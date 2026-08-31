// Package sms defines the optional SMS delivery boundary used by MFA flows.
package sms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sms_delivery_attempts
			WHERE (user_id = ? OR destination = ?) AND created_at > ?`, accountID, destination,
			format(now.Add(-window.duration))).Scan(&count); err != nil {
			return 0, fmt.Errorf("count SMS deliveries: %w", err)
		}
		if count >= window.maximum {
			return window.duration, ErrRateLimited
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sms_delivery_attempts (id, user_id, destination, created_at) VALUES (?, ?, ?, ?)", "sms_"+uuid.NewString(), accountID, destination, format(now)); err != nil {
		return 0, fmt.Errorf("reserve SMS delivery: %w", err)
	}
	return 0, nil
}

func format(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
