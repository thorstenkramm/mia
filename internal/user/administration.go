package user

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrAdministrationUnauthorized  = errors.New("account administration unauthorized")
	ErrAccountQueryInvalid         = errors.New("invalid account query")
	ErrAccountPreconditionRequired = errors.New("account precondition required")
	ErrAccountPreconditionFailed   = errors.New("account precondition failed")
)

type AccountClass string
type AccountState string

const (
	ClassStaff       AccountClass = "staff"
	ClassStudentOnly AccountClass = "student_only"
	StateActive      AccountState = "active"
	StateBanned      AccountState = "banned"
)

type AccountFilter struct {
	Username, Class, Role, State string
	Limit, Offset                int
}

type AdministrationAccount struct {
	ID, Username string
	Class        AccountClass
	State        AccountState
	Roles        []Role
	Actions      AdministrationActions
	ETag         string
}

type ActionEligibility struct {
	Eligible     bool     `json:"eligible"`
	Blockers     []string `json:"blockers"`
	Consequences []string `json:"consequences"`
}

type RoleGrantEligibility struct {
	Administrator ActionEligibility `json:"administrator"`
	Supervisor    ActionEligibility `json:"supervisor"`
}

type AdministrationActions struct {
	GrantRole RoleGrantEligibility `json:"grant_role"`
	Delete    ActionEligibility    `json:"delete"`
}

type AdministrationList struct {
	Accounts []AdministrationAccount
	HasMore  bool
}

type administrationSnapshot struct {
	AdministrationAccount
	EmailVerified, SoleSupervisor bool
	AdministratorCount            int
}

// ListAdministrationAccounts returns a minimal administrator-only global page.
func ListAdministrationAccounts(ctx context.Context, query miSQLite.Querier, actorID string,
	filter AccountFilter, soleSupervisor SoleSupervisorCheck) (AdministrationList, error) {
	if err := requireAdministration(ctx, query, actorID); err != nil {
		return AdministrationList{}, err
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 10_000 {
		return AdministrationList{}, ErrAccountQueryInvalid
	}
	conditions := []string{"1 = 1"}
	args := make([]any, 0, 6)
	if filter.Username != "" {
		key, err := identity.Username(filter.Username)
		if err != nil {
			return AdministrationList{}, ErrAccountQueryInvalid
		}
		conditions = append(conditions, "u.username_key = ?")
		args = append(args, key)
	}
	if filter.Class != "" {
		switch AccountClass(filter.Class) {
		case ClassStaff:
			conditions = append(conditions, "EXISTS(SELECT 1 FROM user_roles sr WHERE sr.user_id = u.id AND sr.role IN ('administrator','supervisor','mentor'))")
		case ClassStudentOnly:
			conditions = append(conditions, "EXISTS(SELECT 1 FROM user_roles cr WHERE cr.user_id = u.id AND cr.role = 'student') AND NOT EXISTS(SELECT 1 FROM user_roles sr WHERE sr.user_id = u.id AND sr.role IN ('administrator','supervisor','mentor'))")
		default:
			return AdministrationList{}, ErrAccountQueryInvalid
		}
	}
	if filter.Role != "" {
		role := Role(filter.Role)
		if role != Administrator && role != Supervisor && role != Mentor && role != Student {
			return AdministrationList{}, ErrAccountQueryInvalid
		}
		conditions = append(conditions, "EXISTS(SELECT 1 FROM user_roles rr WHERE rr.user_id = u.id AND rr.role = ?)")
		args = append(args, role)
	}
	if filter.State != "" {
		switch AccountState(filter.State) {
		case StateActive:
			conditions = append(conditions, "u.is_banned = 0")
		case StateBanned:
			conditions = append(conditions, "u.is_banned = 1")
		default:
			return AdministrationList{}, ErrAccountQueryInvalid
		}
	}
	args = append(args, filter.Limit+1, filter.Offset)
	statement := `SELECT u.id FROM users u WHERE ` + strings.Join(conditions, " AND ") +
		` ORDER BY u.username_key, u.id LIMIT ? OFFSET ?`
	rows, err := query.QueryContext(ctx, statement, args...)
	if err != nil {
		return AdministrationList{}, fmt.Errorf("list administration accounts: %w", err)
	}
	ids, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return AdministrationList{}, fmt.Errorf("collect administration accounts: %w", err)
	}
	hasMore := len(ids) > filter.Limit
	if hasMore {
		ids = ids[:filter.Limit]
	}
	accounts := make([]AdministrationAccount, 0, len(ids))
	for _, id := range ids {
		snapshot, err := loadAdministrationSnapshot(ctx, query, id, actorID, soleSupervisor)
		if err != nil {
			return AdministrationList{}, err
		}
		accounts = append(accounts, snapshot.AdministrationAccount)
	}
	return AdministrationList{Accounts: accounts, HasMore: hasMore}, nil
}

// GetAdministrationAccount returns one minimal administrator-only target projection.
func GetAdministrationAccount(ctx context.Context, query miSQLite.Querier, actorID,
	accountID string, soleSupervisor SoleSupervisorCheck) (AdministrationAccount, error) {
	if err := requireAdministration(ctx, query, actorID); err != nil {
		return AdministrationAccount{}, err
	}
	snapshot, err := loadAdministrationSnapshot(ctx, query, accountID, actorID, soleSupervisor)
	if errors.Is(err, sql.ErrNoRows) {
		return AdministrationAccount{}, ErrDeletionNotFound
	}
	return snapshot.AdministrationAccount, err
}

// RequireReviewedRoleGrant validates the effect-complete account detail ETag in the caller's transaction.
func RequireReviewedRoleGrant(ctx context.Context, query miSQLite.Querier, accountID, actorID,
	expected string, soleSupervisor SoleSupervisorCheck) error {
	if err := requireAdministration(ctx, query, actorID); err != nil {
		return ErrRoleActorUnauthorized
	}
	snapshot, err := loadAdministrationSnapshot(ctx, query, accountID, actorID, soleSupervisor)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRoleRecipientIneligible
	}
	if err != nil {
		return err
	}
	if expected == "" {
		return ErrAccountPreconditionRequired
	}
	if expected != snapshot.ETag {
		return ErrAccountPreconditionFailed
	}
	return nil
}

func requireAdministration(ctx context.Context, query miSQLite.Querier, actorID string) error {
	admin, err := HasRole(ctx, query, actorID, Administrator)
	if err != nil {
		return err
	}
	if !admin {
		return ErrAdministrationUnauthorized
	}
	return nil
}

func loadAdministrationSnapshot(ctx context.Context, query miSQLite.Querier, accountID,
	actorID string, soleSupervisor SoleSupervisorCheck) (administrationSnapshot, error) {
	var snapshot administrationSnapshot
	var banned, verified int
	if err := query.QueryRowContext(ctx, `SELECT id, username, is_banned, email_verified_at IS NOT NULL
		FROM users WHERE id = ?`, accountID).Scan(&snapshot.ID, &snapshot.Username, &banned, &verified); err != nil {
		return administrationSnapshot{}, err
	}
	roles, err := Roles(ctx, query, accountID)
	if err != nil {
		return administrationSnapshot{}, err
	}
	snapshot.Roles = roles
	snapshot.EmailVerified = verified != 0
	snapshot.State = StateActive
	if banned != 0 {
		snapshot.State = StateBanned
	}
	staff, administrator := hasStaffRole(roles), containsRole(roles, Administrator)
	snapshot.Class = ClassStudentOnly
	if staff {
		snapshot.Class = ClassStaff
	}
	if err := query.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_roles WHERE role = 'administrator'").
		Scan(&snapshot.AdministratorCount); err != nil {
		return administrationSnapshot{}, fmt.Errorf("count administrators for account review: %w", err)
	}
	if soleSupervisor != nil {
		snapshot.SoleSupervisor, err = soleSupervisor(ctx, query, accountID)
		if err != nil {
			return administrationSnapshot{}, err
		}
	}
	grantBlockers := []string{}
	if !staff || !snapshot.EmailVerified || snapshot.State == StateBanned {
		grantBlockers = append(grantBlockers, "registered_verified_staff_required")
	}
	snapshot.Actions.GrantRole.Administrator = eligibility(grantBlockers,
		[]string{"grant_administrator_role"})
	snapshot.Actions.GrantRole.Supervisor = eligibility(grantBlockers,
		[]string{"grant_supervisor_role", "grant_student_role"})
	deleteBlockers := []string{}
	if staff && actorID == accountID {
		deleteBlockers = append(deleteBlockers, "different_administrator_required")
	}
	if administrator && snapshot.AdministratorCount <= 1 {
		deleteBlockers = append(deleteBlockers, "last_administrator")
	}
	if snapshot.SoleSupervisor {
		deleteBlockers = append(deleteBlockers, "sole_course_supervisor")
	}
	consequences := []string{"delete_account_operational_data", "deidentify_audit_history"}
	if staff {
		consequences = append(consequences, "remove_assignments", "triage_open_work", "clear_actor_references")
	}
	snapshot.Actions.Delete = eligibility(deleteBlockers, consequences)
	snapshot.ETag = administrationETag(snapshot, actorID)
	return snapshot, nil
}

func eligibility(blockers, consequences []string) ActionEligibility {
	if blockers == nil {
		blockers = []string{}
	}
	return ActionEligibility{Eligible: len(blockers) == 0, Blockers: blockers, Consequences: consequences}
}

func administrationETag(snapshot administrationSnapshot, actorID string) string {
	roles := make([]string, len(snapshot.Roles))
	for index, role := range snapshot.Roles {
		roles[index] = string(role)
	}
	sort.Strings(roles)
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t\x00%d\x00%t\x00%s", snapshot.ID,
		snapshot.Username, snapshot.State, snapshot.Class, snapshot.EmailVerified, snapshot.AdministratorCount,
		snapshot.SoleSupervisor, actorID+"\x00"+strings.Join(roles, ","))
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("\"%x\"", digest)
}

func hasStaffRole(roles []Role) bool {
	return containsRole(roles, Administrator) || containsRole(roles, Supervisor) || containsRole(roles, Mentor)
}

func containsRole(roles []Role, wanted Role) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}
