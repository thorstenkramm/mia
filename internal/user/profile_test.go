package user_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/auth"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	"github.com/thorstenkramm/mia/internal/user"
)

type recordingSMS struct {
	destination, code string
	err               error
}

func (sender *recordingSMS) Send(_ context.Context, destination, code string) error {
	sender.destination, sender.code = destination, code
	return sender.err
}

func TestStaffProfileUpdateIsNormalizedAuditedAndStudentSelfEditRejected(t *testing.T) {
	database := openDatabase(t)
	staff := createStaff(t, database, "staff", []user.Role{user.Mentor})
	student := createStudent(t, database, "student")
	service := user.NewService(database, t.TempDir(), &recordingSMS{}, auth.InvalidatePendingSMS)
	name := "  Staff Name  "
	voice := "voice-123"
	language, country, zone := "de-de", "us", "Europe/Berlin"
	profile, err := service.UpdateSelf(context.Background(), staff.ID, user.ProfileChanges{
		Name: user.OptionalString{Set: true, Value: &name}, TTSVoice: user.OptionalString{Set: true, Value: &voice},
		PreferredLanguage: &language, Country: &country, TimeZone: &zone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name == nil || *profile.Name != "Staff Name" || profile.PreferredLanguage != "de-DE" ||
		profile.Country != "US" || profile.TimeZone != zone {
		t.Fatalf("normalized profile = %+v", profile)
	}
	if _, err := service.UpdateSelf(context.Background(), student.ID,
		user.ProfileChanges{Name: user.OptionalString{Set: true, Value: &name}}); !errors.Is(err, user.ErrProfileNotStaff) {
		t.Fatalf("student profile update error = %v", err)
	}
	var events int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'user.profile.updated' AND subject_user_id = ?",
		staff.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("profile audit events = %d", events)
	}
}

func TestMobileVerificationChangesProfileOnceAndPreservesActiveFactorDestination(t *testing.T) {
	database := openDatabase(t)
	staff := createStaff(t, database, "staff", []user.Role{user.Mentor})
	sender := &recordingSMS{}
	service := user.NewService(database, t.TempDir(), sender, auth.InvalidatePendingSMS)
	oldDestination := "+49111111111"
	if _, err := database.Exec(`UPDATE users SET mobile = ?, mobile_verified_at = ? WHERE id = ?`, oldDestination,
		"2026-08-31T12:00:00.000000Z", staff.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_factors
		(user_id, method, sms_destination, created_at) VALUES (?, 'sms', ?, ?)`, staff.ID, oldDestination,
		"2026-08-31T12:00:00.000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_enrollments
		(id, user_id, method, sms_destination, sms_code, expires_at, created_at)
		VALUES (?, ?, 'sms', ?, '654321', ?, ?)`, "mfe_"+uuid.NewString(), staff.ID, oldDestination,
		"2099-01-01T00:30:00.000000Z", "2099-01-01T00:00:00.000000Z"); err != nil {
		t.Fatal(err)
	}
	challengeID, err := service.StartMobileChallenge(context.Background(), staff.ID, "+49222222222")
	if err != nil {
		t.Fatal(err)
	}
	if sender.code == "" || sender.destination != "+49222222222" {
		t.Fatal("mobile code was not sent to the pending destination")
	}
	issuedCode := sender.code
	var expiryBefore string
	var failuresBefore int
	if err := database.QueryRow(`SELECT expires_at, failed_attempts FROM mobile_verification_challenges WHERE id = ?`,
		challengeID).Scan(&expiryBefore, &failuresBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE sms_delivery_attempts SET created_at = ? WHERE user_id = ?",
		time.Now().Add(-2*time.Minute).UTC().Format("2006-01-02T15:04:05.000000Z"), staff.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ResendMobileChallenge(context.Background(), staff.ID, challengeID); err != nil {
		t.Fatal(err)
	}
	var expiryAfter string
	var failuresAfter int
	if err := database.QueryRow(`SELECT expires_at, failed_attempts FROM mobile_verification_challenges WHERE id = ?`,
		challengeID).Scan(&expiryAfter, &failuresAfter); err != nil {
		t.Fatal(err)
	}
	if sender.code != issuedCode || expiryAfter != expiryBefore || failuresAfter != failuresBefore {
		t.Fatal("mobile resend changed the code, expiry, or failure count")
	}
	if err := service.VerifyMobileChallenge(context.Background(), staff.ID, challengeID, wrongCode(sender.code)); !errors.Is(err, user.ErrMobileCode) {
		t.Fatalf("wrong-code error = %v", err)
	}
	if err := service.VerifyMobileChallenge(context.Background(), staff.ID, challengeID, sender.code); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyMobileChallenge(context.Background(), staff.ID, challengeID, sender.code); !errors.Is(err, user.ErrMobileChallenge) {
		t.Fatalf("replay error = %v", err)
	}
	var mobile, activeDestination string
	if err := database.QueryRow("SELECT mobile FROM users WHERE id = ?", staff.ID).Scan(&mobile); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT sms_destination FROM mfa_factors WHERE user_id = ?", staff.ID).
		Scan(&activeDestination); err != nil {
		t.Fatal(err)
	}
	if mobile != "+49222222222" || activeDestination != oldDestination {
		t.Fatalf("profile/factor destinations = %q/%q", mobile, activeDestination)
	}
	var pending int
	if err := database.QueryRow("SELECT COUNT(*) FROM mfa_enrollments WHERE user_id = ? AND method = 'sms'", staff.ID).
		Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatal("pending SMS enrollment survived profile-mobile change")
	}
	var sensitive int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE metadata LIKE '%49222222222%'
		OR metadata LIKE ?`, "%"+sender.code+"%").Scan(&sensitive); err != nil {
		t.Fatal(err)
	}
	if sensitive != 0 {
		t.Fatal("audit retained a mobile number or code")
	}
}

func TestMobileFifthFailureInvalidatesAndProviderFailureLeavesCurrentMobile(t *testing.T) {
	database := openDatabase(t)
	staff := createStaff(t, database, "staff", []user.Role{user.Mentor})
	sender := &recordingSMS{}
	service := user.NewService(database, t.TempDir(), sender, auth.InvalidatePendingSMS)
	challengeID, err := service.StartMobileChallenge(context.Background(), staff.ID, "+49333333333")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 5 {
		err := service.VerifyMobileChallenge(context.Background(), staff.ID, challengeID, wrongCode(sender.code))
		if !errors.Is(err, user.ErrMobileCode) {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	if err := service.VerifyMobileChallenge(context.Background(), staff.ID, challengeID, sender.code); !errors.Is(err, user.ErrMobileChallenge) {
		t.Fatalf("invalidated challenge error = %v", err)
	}
	if _, err := database.Exec("UPDATE sms_delivery_attempts SET created_at = ? WHERE user_id = ?",
		time.Now().Add(-2*time.Minute).UTC().Format("2006-01-02T15:04:05.000000Z"), staff.ID); err != nil {
		t.Fatal(err)
	}
	expiredID, err := service.StartMobileChallenge(context.Background(), staff.ID, "+49444444444")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE mobile_verification_challenges SET expires_at = ? WHERE id = ?",
		time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000000Z"), expiredID); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyMobileChallenge(context.Background(), staff.ID, expiredID, sender.code); !errors.Is(err, user.ErrMobileChallenge) {
		t.Fatalf("expired challenge error = %v", err)
	}
	if _, err := database.Exec("UPDATE sms_delivery_attempts SET created_at = ? WHERE user_id = ?",
		time.Now().Add(-2*time.Minute).UTC().Format("2006-01-02T15:04:05.000000Z"), staff.ID); err != nil {
		t.Fatal(err)
	}
	failing := &recordingSMS{err: sms.ErrUnavailable}
	service = user.NewService(database, t.TempDir(), failing, auth.InvalidatePendingSMS)
	if _, err := service.StartMobileChallenge(context.Background(), staff.ID, "+49555555555"); !errors.Is(err, user.ErrMobileUnavailable) {
		t.Fatalf("provider failure error = %v", err)
	}
	var mobile sql.NullString
	if err := database.QueryRow("SELECT mobile FROM users WHERE id = ?", staff.ID).Scan(&mobile); err != nil {
		t.Fatal(err)
	}
	if mobile.Valid {
		t.Fatalf("provider failure changed mobile to %q", mobile.String)
	}
}

func wrongCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func TestMobileRemovalClearsPendingStateButNotActiveFactor(t *testing.T) {
	database := openDatabase(t)
	staff := createStaff(t, database, "staff", []user.Role{user.Mentor})
	service := user.NewService(database, t.TempDir(), &recordingSMS{}, auth.InvalidatePendingSMS)
	if _, err := database.Exec(`UPDATE users SET mobile = '+49111111111', mobile_verified_at = ? WHERE id = ?`,
		"2026-08-31T12:00:00.000000Z", staff.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO mfa_factors (user_id, method, sms_destination, created_at)
		VALUES (?, 'sms', '+49111111111', ?)`, staff.ID, "2026-08-31T12:00:00.000000Z"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveMobile(context.Background(), staff.ID); err != nil {
		t.Fatal(err)
	}
	var mobile sql.NullString
	var destination string
	if err := database.QueryRow("SELECT mobile FROM users WHERE id = ?", staff.ID).Scan(&mobile); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT sms_destination FROM mfa_factors WHERE user_id = ?", staff.ID).Scan(&destination); err != nil {
		t.Fatal(err)
	}
	if mobile.Valid || destination != "+49111111111" {
		t.Fatalf("removed mobile/active destination = %v/%q", mobile, destination)
	}
}
