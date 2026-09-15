package user

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrAdministrationUnauthorized  = errors.New("account administration unauthorized")
	ErrAccountQueryInvalid         = errors.New("invalid account query")
	ErrAccountPreconditionRequired = errors.New("account precondition required")
	ErrAccountPreconditionFailed   = errors.New("account precondition failed")
	ErrMentorTargetUnavailable     = errors.New("mentor target unavailable")
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

type MentorGrantState string

const (
	MentorGrantAvailable      MentorGrantState = "available"
	MentorGrantAlreadyGranted MentorGrantState = "already_granted"
)

// MentorTarget is the minimal wrong-person-prevention projection available to
// supervisors. DisplayName is the sole optional identity field.
type MentorTarget struct {
	ID, Username string
	DisplayName  *string
	GrantState   MentorGrantState
	ETag         string
}

type mentorTargetSnapshot struct {
	MentorTarget
	EmailVerified, Staff, Banned, Mentor bool
}

// ResolveMentorTarget resolves one exact eligible staff account for a current
// supervisor without exposing the global account directory.
func ResolveMentorTarget(ctx context.Context, query miSQLite.Querier, actorID, targetID string) (MentorTarget, error) {
	if err := requireMentorGrantActor(ctx, query, actorID); err != nil {
		return MentorTarget{}, err
	}
	if !validUserID(targetID) {
		return MentorTarget{}, ErrMentorTargetUnavailable
	}
	snapshot, err := loadMentorTargetSnapshot(ctx, query, actorID, targetID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !snapshot.eligible() {
		return MentorTarget{}, ErrMentorTargetUnavailable
	}
	if err != nil {
		return MentorTarget{}, err
	}
	return snapshot.MentorTarget, nil
}

// RequireReviewedMentorGrant validates a supervisor's focused mentor-target
// preflight inside the role-grant transaction. Once a validator was supplied,
// target disappearance or ineligibility is stale state rather than a new
// disclosure outcome.
func RequireReviewedMentorGrant(ctx context.Context, query miSQLite.Querier, actorID, targetID, expected string) error {
	if err := requireMentorGrantActor(ctx, query, actorID); err != nil {
		return err
	}
	if expected == "" {
		return ErrAccountPreconditionRequired
	}
	if !validUserID(targetID) {
		return ErrAccountPreconditionFailed
	}
	snapshot, err := loadMentorTargetSnapshot(ctx, query, actorID, targetID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!snapshot.eligible() || snapshot.ETag != expected) {
		return ErrAccountPreconditionFailed
	}
	return err
}

// requireMentorGrantActor enforces supervisor authority before any prospective
// target lookup, preventing the focused resolver from exposing staff identity.
func requireMentorGrantActor(ctx context.Context, query miSQLite.Querier, actorID string) error {
	allowed, err := HasRole(ctx, query, actorID, Supervisor)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRoleActorUnauthorized
	}
	return nil
}

func loadMentorTargetSnapshot(ctx context.Context, query miSQLite.Querier, actorID,
	targetID string) (mentorTargetSnapshot, error) {
	var snapshot mentorTargetSnapshot
	var displayName sql.NullString
	var verified, staff, banned, mentor int
	err := query.QueryRowContext(ctx, `SELECT u.id, u.username, u.name, u.email_verified_at IS NOT NULL, u.is_banned,
		EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id
			AND r.role IN ('administrator','supervisor','mentor')),
		EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'mentor')
		FROM users u WHERE u.id = ?`, targetID).
		Scan(&snapshot.ID, &snapshot.Username, &displayName, &verified, &banned, &staff, &mentor)
	if err != nil {
		return mentorTargetSnapshot{}, err
	}
	if displayName.Valid {
		snapshot.DisplayName = &displayName.String
	}
	snapshot.EmailVerified = verified != 0
	snapshot.Staff = staff != 0
	snapshot.Banned = banned != 0
	snapshot.Mentor = mentor != 0
	snapshot.GrantState = MentorGrantAvailable
	if snapshot.Mentor {
		snapshot.GrantState = MentorGrantAlreadyGranted
	}
	snapshot.ETag = mentorTargetETag(snapshot, actorID)
	return snapshot, nil
}

func (snapshot mentorTargetSnapshot) eligible() bool {
	return snapshot.EmailVerified && snapshot.Staff && !snapshot.Banned
}

// mentorTargetETag binds the reviewed identity and eligibility state to the
// requesting supervisor so a validator cannot authorize another actor or stale target.
func mentorTargetETag(snapshot mentorTargetSnapshot, actorID string) string {
	displayName := ""
	if snapshot.DisplayName != nil {
		displayName = *snapshot.DisplayName
	}
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%t\x00%t\x00%t\x00%t\x00%s", snapshot.ID,
		snapshot.Username, displayName, snapshot.Staff, snapshot.EmailVerified, snapshot.Banned, snapshot.Mentor, actorID)
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("\"%x\"", digest)
}

// validUserID accepts only MIA's canonical lowercase UUID-v4 account IDs,
// preventing alternate encodings from reaching target lookup or limiter state.
func validUserID(value string) bool {
	if len(value) != 38 || !strings.HasPrefix(value, "u_") {
		return false
	}
	parsed, err := uuid.Parse(value[2:])
	return err == nil && parsed.Version() == 4 && parsed.String() == value[2:]
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
