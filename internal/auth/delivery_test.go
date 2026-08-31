package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

func TestDeliveryOutcomesRecordChallengeEffects(t *testing.T) {
	cases := []struct {
		name        string
		sendError   error
		consumed    bool
		auditAction string
	}{
		{"definite rejection invalidates the challenge", smtp.ErrRejected, true, "auth.password_recovery.delivery_failed"},
		{"timeout leaves the challenge usable", smtp.ErrTimeout, false, "auth.password_recovery.timed_out"},
		{"ambiguous outcome leaves the challenge usable", errors.New("connection reset"), false, "auth.password_recovery.ambiguous"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database, accountID, challengeID := deliveryFixture(t)
			manager := NewDeliveryManager(database, recoveryMailerFunc(func(context.Context, string, string) error {
				return testCase.sendError
			}), nil)
			if !manager.Admit(context.Background(), accountID, "staff@example.test", "https://mia.test/password-reset#token=x", challengeID) {
				t.Fatal("delivery admission failed before shutdown")
			}
			manager.Close()
			var consumed any
			if err := database.QueryRow("SELECT consumed_at FROM password_reset_challenges WHERE id = ?", challengeID).Scan(&consumed); err != nil {
				t.Fatal(err)
			}
			if (consumed != nil) != testCase.consumed {
				t.Fatalf("challenge consumed=%v, expected %v", consumed != nil, testCase.consumed)
			}
			assertAuditCount(t, database, testCase.auditAction, 1)
		})
	}
}

func TestDeliveryManagerRefusesAdmissionAfterClose(t *testing.T) {
	database, accountID, challengeID := deliveryFixture(t)
	manager := NewDeliveryManager(database, recoveryMailerFunc(func(context.Context, string, string) error {
		return nil
	}), nil)
	manager.Close()
	manager.Close()
	if manager.Admit(context.Background(), accountID, "staff@example.test", "https://mia.test/password-reset#token=x", challengeID) {
		t.Fatal("delivery admitted after shutdown")
	}
}

func deliveryFixture(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	database, err := miSQLite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	hash, err := identity.Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var account user.Account
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		var createErr error
		account, createErr = user.Create(context.Background(), tx, user.CreateInput{Username: "staff", Email: "staff@example.test", PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", EmailVerified: true, Roles: []user.Role{user.Administrator}})
		return createErr
	}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("token"))
	challengeID := "prc_delivery_test"
	now := time.Now().UTC()
	if _, err := database.Exec(`INSERT INTO password_reset_challenges (id, user_id, token_digest, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)`, challengeID, account.ID, digest[:], instant(now.Add(30*time.Minute)), instant(now)); err != nil {
		t.Fatal(err)
	}
	return database, account.ID, challengeID
}
