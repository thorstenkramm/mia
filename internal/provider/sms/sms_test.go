package sms_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (roundTrip roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestClickSendClientUsesBoundedAllowlistedRequestAndResponse(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v3/sms/send" {
			t.Errorf("ClickSend request = %s %s", request.Method, request.URL.Path)
		}
		username, key, ok := request.BasicAuth()
		if !ok || username != "account" || key != "secret" {
			t.Error("ClickSend basic authentication missing")
		}
		var body struct {
			Messages []struct {
				To, Body, From string
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 1 || body.Messages[0].To != "+49123456789" ||
			!strings.Contains(body.Messages[0].Body, "123456") || body.Messages[0].From != "MIA" {
			t.Errorf("ClickSend request = %+v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write([]byte(`{"response_code":"SUCCESS","data":{"messages":[{"status":"SUCCESS"}]}}`)); err != nil {
			t.Error(err)
		}
	}))
	defer provider.Close()
	sender := sms.New(sms.ClientOptions{Username: "account", APIKey: "secret", SenderID: "MIA",
		BaseURL: provider.URL + "/v3/", HTTPClient: provider.Client()})
	if err := sender.Send(context.Background(), "+49123456789", "123456"); err != nil {
		t.Fatal(err)
	}
}

func TestClickSendClientRejectsMalformedOrFailedProviderOutcome(t *testing.T) {
	for name, responseBody := range map[string]string{
		"malformed":       `{}`,
		"message failure": `{"response_code":"SUCCESS","data":{"messages":[{"status":"INVALID_RECIPIENT"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if _, err := response.Write([]byte(responseBody)); err != nil {
					t.Error(err)
				}
			}))
			defer provider.Close()
			sender := sms.New(sms.ClientOptions{Username: "account", APIKey: "secret", BaseURL: provider.URL,
				HTTPClient: provider.Client()})
			if err := sender.Send(context.Background(), "+49123456789", "123456"); err == nil {
				t.Fatal("provider failure was accepted")
			}
		})
	}
}

func TestClickSendClientDoesNotForwardCredentialsThroughRedirects(t *testing.T) {
	targetReached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetReached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	sender := sms.New(sms.ClientOptions{Username: "account", APIKey: "secret", BaseURL: redirect.URL})
	if err := sender.Send(context.Background(), "+49123456789", "123456"); err == nil {
		t.Fatal("ClickSend redirect was accepted")
	}
	if targetReached {
		t.Fatal("ClickSend credentials were forwarded through a redirect")
	}
}

func TestClickSendClientJoinsDefaultAndTrailingBaseURLs(t *testing.T) {
	for name, baseURL := range map[string]string{
		"default":    "",
		"root slash": "http://127.0.0.1:3550/",
		"v3 slash":   "http://127.0.0.1:3550/v3/",
	} {
		t.Run(name, func(t *testing.T) {
			var gotURL string
			client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
				gotURL = request.URL.String()
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
					`{"response_code":"SUCCESS","data":{"messages":[{"status":"SUCCESS"}]}}`)), Header: make(http.Header)}, nil
			})}
			sender := sms.New(sms.ClientOptions{Username: "account", APIKey: "secret", BaseURL: baseURL,
				HTTPClient: client})
			if err := sender.Send(context.Background(), "+49123456789", "123456"); err != nil {
				t.Fatal(err)
			}
			want := "https://rest.clicksend.com/v3/sms/send"
			if name == "root slash" {
				want = "http://127.0.0.1:3550/sms/send"
			}
			if name == "v3 slash" {
				want = "http://127.0.0.1:3550/v3/sms/send"
			}
			if gotURL != want {
				t.Fatalf("ClickSend URL = %q, want %q", gotURL, want)
			}
		})
	}
}

func TestReserveAppliesAccountAndDestinationLimitsIndependently(t *testing.T) {
	database, first, second := smsDatabase(t)
	base := time.Now().Add(-2 * time.Hour)
	for index := range 4 {
		reserve(t, database, first, "+49111111111", base.Add(time.Duration(index)*2*time.Minute))
		reserve(t, database, second, "+49222222222", base.Add(time.Duration(index)*2*time.Minute))
	}
	// Account first has four sends and destination two has four sends. Their
	// union has eight rows, but each independent dimension still permits one.
	reserve(t, database, first, "+49222222222", base.Add(20*time.Minute))
}

func reserve(t *testing.T, database *sql.DB, accountID, destination string, at time.Time) {
	t.Helper()
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		_, err := sms.Reserve(context.Background(), tx, accountID, destination, at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func smsDatabase(t *testing.T) (*sql.DB, string, string) {
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
	ids := make([]string, 2)
	for index, username := range []string{"first", "second"} {
		if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
			account, createErr := user.Create(context.Background(), tx, user.CreateInput{Username: username,
				PasswordHash: hash, Language: "en", Country: "DE", TimeZone: "UTC", Roles: []user.Role{user.Student}})
			ids[index] = account.ID
			return createErr
		}); err != nil {
			t.Fatal(err)
		}
	}
	return database, ids[0], ids[1]
}
