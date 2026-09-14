// Package sms defines the optional SMS delivery boundary used by MFA flows.
package sms

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

// ErrUnavailable identifies an SMS operation when no provider is configured.
var ErrUnavailable = errors.New("SMS provider is unavailable")

// ErrRateLimited means the durable SMS delivery gate rejected a send.
var ErrRateLimited = errors.New("SMS delivery rate limited")

// LimitReason identifies a safe class of SMS delivery denial without exposing
// the account or destination dimension that caused it.
type LimitReason string

const (
	LimitNone     LimitReason = ""
	LimitCooldown LimitReason = "cooldown"
	LimitQuota    LimitReason = "rate_limited"
)

// Eligibility is the authoritative durable SMS delivery state at one instant.
type Eligibility struct {
	Allowed bool
	Reason  LimitReason
	RetryAt time.Time
}

// LimitError preserves safe retry timing for HTTP callers.
type LimitError struct {
	Eligibility Eligibility
	RetryAfter  time.Duration
}

func (err *LimitError) Error() string { return ErrRateLimited.Error() }

func (err *LimitError) Unwrap() error { return ErrRateLimited }

// Sender delivers a security code to an already validated destination.
type Sender interface {
	Send(context.Context, string, string) error
}

// Client sends security codes through the ClickSend v3 API. It is safe for concurrent use.
type Client struct {
	username, apiKey, senderID, endpoint string
	httpClient                           *http.Client
}

// ClientOptions configures a ClickSend adapter. HTTPClient is injectable only so
// automated tests never contact production providers.
type ClientOptions struct {
	Username, APIKey, SenderID, BaseURL string
	HTTPClient                          *http.Client
}

// New constructs a ClickSend sender, or Unavailable when credentials are absent.
func New(options ClientOptions) Sender {
	if options.Username == "" || options.APIKey == "" {
		return Unavailable{}
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = "https://rest.clicksend.com/v3"
	}
	endpoint := strings.TrimSuffix(baseURL, "/") + "/sms/send"
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		}, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("ClickSend redirects are not allowed")
		}}
	}
	return &Client{username: options.Username, apiKey: options.APIKey, senderID: options.SenderID,
		endpoint: endpoint, httpClient: httpClient}
}

// Send performs one non-retried provider request and accepts only an explicit
// API-level and per-message SUCCESS response.
func (client *Client) Send(ctx context.Context, destination, code string) (returnErr error) {
	type smsMessage struct {
		To   string `json:"to"`
		Body string `json:"body"`
		From string `json:"from,omitempty"`
	}
	message := struct {
		Messages []smsMessage `json:"messages"`
	}{}
	message.Messages = append(message.Messages, smsMessage{
		To: destination, Body: "Your MIA verification code is " + code + ".", From: client.senderID,
	})
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal ClickSend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ClickSend request: %w", err)
	}
	request.SetBasicAuth(client.username, client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send ClickSend request: %w", ErrUnavailable)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close ClickSend response: %w", ErrUnavailable))
		}
	}()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(responseBody) > 1<<20 || response.StatusCode != http.StatusOK {
		return fmt.Errorf("read ClickSend response: %w", ErrUnavailable)
	}
	var result struct {
		ResponseCode string `json:"response_code"`
		Data         struct {
			Messages []struct {
				Status string `json:"status"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil || result.ResponseCode != "SUCCESS" ||
		len(result.Data.Messages) != 1 || result.Data.Messages[0].Status != "SUCCESS" {
		return fmt.Errorf("invalid ClickSend response: %w", ErrUnavailable)
	}
	return nil
}

// Available reports whether sender can attempt an SMS delivery.
func Available(sender Sender) bool {
	if sender == nil {
		return false
	}
	switch sender.(type) {
	case Unavailable, *Unavailable:
		return false
	default:
		return true
	}
}

// Unavailable is the default sender when ClickSend is not configured.
type Unavailable struct{}

// Send never attempts a network request.
func (Unavailable) Send(context.Context, string, string) error { return ErrUnavailable }

// Reserve records an SMS delivery attempt before the provider is contacted.
// Failed and ambiguous sends deliberately consume the same durable quota.
func Reserve(ctx context.Context, tx *sql.Tx, accountID, destination string, now time.Time) (time.Duration, error) {
	eligibility, err := Check(ctx, tx, accountID, destination, now)
	if err != nil {
		return 0, err
	}
	if !eligibility.Allowed {
		retry := eligibility.RetryAt.Sub(now.UTC())
		return retry, &LimitError{Eligibility: eligibility, RetryAfter: retry}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sms_delivery_attempts (id, user_id, destination, created_at) VALUES (?, ?, ?, ?)", "sms_"+uuid.NewString(), accountID, destination, format(now)); err != nil {
		return 0, fmt.Errorf("reserve SMS delivery: %w", err)
	}
	return 0, nil
}

// CanSend reports current durable SMS eligibility without reserving capacity.
func CanSend(ctx context.Context, query miSQLite.Querier, accountID, destination string, now time.Time) (bool, error) {
	eligibility, err := Check(ctx, query, accountID, destination, now)
	return eligibility.Allowed, err
}

// Check projects cooldown and quota eligibility without consuming capacity.
func Check(ctx context.Context, query miSQLite.Querier, accountID, destination string,
	now time.Time,
) (result Eligibility, returnErr error) {
	now = now.UTC()
	rows, err := query.QueryContext(ctx, `SELECT user_id, destination, created_at FROM sms_delivery_attempts
		WHERE (user_id = ? OR destination = ?) AND created_at > ? ORDER BY created_at`, accountID, destination,
		format(now.Add(-24*time.Hour)))
	if err != nil {
		return Eligibility{}, fmt.Errorf("load SMS delivery eligibility: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			closeErr := fmt.Errorf("close SMS delivery eligibility rows: %w", err)
			if returnErr == nil {
				returnErr = closeErr
			} else {
				returnErr = errors.Join(returnErr, closeErr)
			}
		}
	}()
	type attempt struct {
		account, destination string
		at                   time.Time
	}
	var attempts []attempt
	for rows.Next() {
		var item attempt
		var stored string
		if err := rows.Scan(&item.account, &item.destination, &stored); err != nil {
			return Eligibility{}, fmt.Errorf("scan SMS delivery eligibility: %w", err)
		}
		item.at, err = time.Parse("2006-01-02T15:04:05.000000Z", stored)
		if err != nil {
			return Eligibility{}, fmt.Errorf("parse SMS delivery instant: %w", err)
		}
		attempts = append(attempts, item)
	}
	if err := rows.Err(); err != nil {
		return Eligibility{}, fmt.Errorf("iterate SMS delivery eligibility: %w", err)
	}
	eligibility := Eligibility{Allowed: true}
	for _, window := range []struct {
		duration time.Duration
		maximum  int
	}{{time.Hour, 5}, {24 * time.Hour, 10}} {
		for _, dimension := range []func(attempt) bool{
			func(item attempt) bool { return item.account == accountID },
			func(item attempt) bool { return item.destination == destination },
		} {
			var matching []time.Time
			for _, item := range attempts {
				if item.at.After(now.Add(-window.duration)) && dimension(item) {
					matching = append(matching, item.at)
				}
			}
			if len(matching) >= window.maximum {
				candidate := matching[len(matching)-window.maximum].Add(window.duration)
				if candidate.After(eligibility.RetryAt) {
					eligibility.RetryAt = candidate
				}
				eligibility.Allowed, eligibility.Reason = false, LimitQuota
			}
		}
	}
	if len(attempts) > 0 {
		cooldownEnd := attempts[len(attempts)-1].at.Add(time.Minute)
		if cooldownEnd.After(now) {
			if cooldownEnd.After(eligibility.RetryAt) {
				eligibility.RetryAt = cooldownEnd
			}
			if eligibility.Reason == LimitNone {
				eligibility.Allowed, eligibility.Reason = false, LimitCooldown
			}
		}
	}
	return eligibility, nil
}

func format(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
