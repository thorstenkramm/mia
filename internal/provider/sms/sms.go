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
	"time"

	"github.com/google/uuid"
)

// ErrUnavailable identifies an SMS operation when no provider is configured.
var ErrUnavailable = errors.New("SMS provider is unavailable")

// ErrRateLimited means the durable SMS delivery gate rejected a send.
var ErrRateLimited = errors.New("SMS delivery rate limited")

// Sender delivers a security code to an already validated destination.
type Sender interface {
	Send(context.Context, string, string) error
}

// Client sends security codes through the ClickSend v3 API. It is safe for concurrent use.
type Client struct {
	username, apiKey, senderID, endpoint string
	httpClient                           *http.Client
}

// ClientOptions configures a ClickSend adapter. Endpoint and HTTPClient are
// injectable only so automated tests never contact production providers.
type ClientOptions struct {
	Username, APIKey, SenderID, Endpoint string
	HTTPClient                           *http.Client
}

// New constructs a ClickSend sender, or Unavailable when credentials are absent.
func New(options ClientOptions) Sender {
	if options.Username == "" || options.APIKey == "" {
		return Unavailable{}
	}
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = "https://rest.clicksend.com/v3/sms/send"
	}
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
	now = now.UTC()
	var newest string
	err := tx.QueryRowContext(ctx, `SELECT created_at FROM sms_delivery_attempts
		WHERE user_id = ? OR destination = ? ORDER BY created_at DESC LIMIT 1`, accountID, destination).Scan(&newest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("load SMS cooldown: %w", err)
	}
	if err == nil {
		last, parseErr := time.Parse("2006-01-02T15:04:05.000000Z", newest)
		if parseErr != nil {
			return 0, fmt.Errorf("parse SMS cooldown: %w", parseErr)
		}
		if retry := time.Minute - now.Sub(last); retry > 0 {
			return retry, ErrRateLimited
		}
	}
	for _, window := range []struct {
		duration time.Duration
		maximum  int
	}{{time.Hour, 5}, {24 * time.Hour, 10}} {
		var accountCount, destinationCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sms_delivery_attempts
			WHERE user_id = ? AND created_at > ?`, accountID,
			format(now.Add(-window.duration))).Scan(&accountCount); err != nil {
			return 0, fmt.Errorf("count SMS deliveries: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sms_delivery_attempts
			WHERE destination = ? AND created_at > ?`, destination,
			format(now.Add(-window.duration))).Scan(&destinationCount); err != nil {
			return 0, fmt.Errorf("count destination SMS deliveries: %w", err)
		}
		if accountCount >= window.maximum || destinationCount >= window.maximum {
			return window.duration, ErrRateLimited
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sms_delivery_attempts (id, user_id, destination, created_at) VALUES (?, ?, ?, ?)", "sms_"+uuid.NewString(), accountID, destination, format(now)); err != nil {
		return 0, fmt.Errorf("reserve SMS delivery: %w", err)
	}
	return 0, nil
}

func format(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
