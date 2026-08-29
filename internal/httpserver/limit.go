package httpserver

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/identity"
)

// CountMode specifies whether a route consumes a limit per request or only on
// an authentication failure.
type CountMode uint8

const (
	CountAttempts CountMode = iota
	CountFailures
)

type limitDefinition struct {
	Window       time.Duration
	Limit, Burst int
	Count        CountMode
	Progressive  bool
	BlockAtLimit bool
}

// LimitName identifies one fixed registry entry. Feature packages cannot define
// new limits or alter the fixed security policy.
type LimitName string

const (
	LimitUnauthenticatedAPI LimitName = "unauthenticated_api_ip"
	LimitLoginIP            LimitName = "login_ip"
	LimitLoginUsername      LimitName = "login_username"
	LimitRecoveryIP         LimitName = "recovery_ip"
	LimitRecoveryIdentifier LimitName = "recovery_identifier"
	LimitInvitationIP       LimitName = "invitation_ip"
	LimitInvitationToken    LimitName = "invitation_token"
	LimitResetIP            LimitName = "reset_ip"
	LimitResetToken         LimitName = "reset_token"
	LimitMFAIP              LimitName = "mfa_ip"
	LimitMFAAccount         LimitName = "mfa_account"
)

var limitRegistry = map[LimitName]limitDefinition{
	LimitUnauthenticatedAPI: {time.Minute, 60, 30, CountAttempts, false, false}, LimitLoginIP: {15 * time.Minute, 30, 0, CountFailures, false, true}, LimitLoginUsername: {15 * time.Minute, 5, 0, CountFailures, true, true}, LimitRecoveryIP: {time.Hour, 10, 0, CountAttempts, false, false}, LimitRecoveryIdentifier: {time.Hour, 3, 0, CountAttempts, false, false}, LimitInvitationIP: {time.Hour, 30, 0, CountAttempts, false, false}, LimitInvitationToken: {time.Hour, 10, 0, CountAttempts, false, false}, LimitResetIP: {time.Hour, 20, 0, CountAttempts, false, false}, LimitResetToken: {time.Hour, 5, 0, CountAttempts, false, false}, LimitMFAIP: {15 * time.Minute, 30, 0, CountAttempts, false, false}, LimitMFAAccount: {15 * time.Minute, 10, 0, CountAttempts, false, false},
}

// Limiter holds a bounded, expiring LRU set of named rolling-window limits. It
// is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	lru      *list.List
}
type limitEntry struct {
	key         string
	events      []time.Time
	tokens      float64
	at, touched time.Time
}
type Result struct {
	Allowed           bool
	RetryAfter, Delay time.Duration
}

func NewLimiter(capacity int) *Limiter {
	return &Limiter{capacity: capacity, entries: make(map[string]*list.Element), lru: list.New()}
}

// Check consumes an attempt-counted definition. Failure-counted definitions are
// recorded only through RecordFailure after the protected operation fails.
func (limiter *Limiter) Check(name LimitName, key string, now time.Time) Result {
	definition, ok := limitRegistry[name]
	if !ok {
		return Result{}
	}
	if definition.Count == CountFailures {
		return limiter.peek(name, definition, key, now)
	}
	return limiter.record(name, definition, key, now)
}

// RecordFailure consumes a failure-counted definition.
func (limiter *Limiter) RecordFailure(name LimitName, key string, now time.Time) Result {
	definition, ok := limitRegistry[name]
	if !ok {
		return Result{}
	}
	if definition.Count != CountFailures {
		return Result{}
	}
	return limiter.record(name, definition, key, now)
}

func (limiter *Limiter) peek(name LimitName, definition limitDefinition, key string, now time.Time) Result {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry := limiter.entry(name, definition, key, now)
	limiter.prune(entry, definition, now)
	return limiter.result(definition, entry, now)
}
func (limiter *Limiter) record(name LimitName, definition limitDefinition, key string, now time.Time) Result {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry := limiter.entry(name, definition, key, now)
	limiter.prune(entry, definition, now)
	result := limiter.result(definition, entry, now)
	if !result.Allowed {
		return result
	}
	if definition.Burst > 0 {
		entry.tokens--
		entry.at = now
	} else {
		entry.events = append(entry.events, now)
	}
	if definition.BlockAtLimit && len(entry.events) >= definition.Limit {
		return Result{RetryAfter: entry.events[0].Add(definition.Window).Sub(now)}
	}
	if definition.Progressive {
		switch len(entry.events) {
		case 2:
			result.Delay = time.Second
		case 3:
			result.Delay = 2 * time.Second
		case 4:
			result.Delay = 4 * time.Second
		}
	}
	return result
}
func (limiter *Limiter) entry(name LimitName, definition limitDefinition, key string, now time.Time) *limitEntry {
	fullKey := string(name) + ":" + key
	element := limiter.entries[fullKey]
	if element == nil {
		limiter.expire(now)
		if limiter.lru.Len() >= limiter.capacity {
			oldest := limiter.lru.Back()
			oldestEntry, ok := oldest.Value.(*limitEntry)
			if !ok {
				panic("invalid limiter entry")
			}
			delete(limiter.entries, oldestEntry.key)
			limiter.lru.Remove(oldest)
		}
		burst := definition.Burst
		element = limiter.lru.PushFront(&limitEntry{key: fullKey, tokens: float64(burst), at: now, touched: now})
		limiter.entries[fullKey] = element
	}
	limiter.lru.MoveToFront(element)
	entry, ok := element.Value.(*limitEntry)
	if !ok {
		panic("invalid limiter entry")
	}
	entry.touched = now
	return entry
}
func (limiter *Limiter) prune(entry *limitEntry, definition limitDefinition, now time.Time) {
	if definition.Burst > 0 {
		entry.tokens = min(float64(definition.Burst), entry.tokens+float64(definition.Limit)*now.Sub(entry.at).Seconds()/definition.Window.Seconds())
		return
	}
	cutoff := now.Add(-definition.Window)
	for len(entry.events) > 0 && !entry.events[0].After(cutoff) {
		entry.events = entry.events[1:]
	}
}
func (limiter *Limiter) result(definition limitDefinition, entry *limitEntry, now time.Time) Result {
	if definition.Burst > 0 {
		if entry.tokens < 1 {
			return Result{RetryAfter: time.Duration((1 - entry.tokens) * float64(definition.Window) / float64(definition.Limit))}
		}
	} else if len(entry.events) >= definition.Limit {
		return Result{RetryAfter: entry.events[0].Add(definition.Window).Sub(now)}
	}
	result := Result{Allowed: true}
	if definition.Progressive {
		switch len(entry.events) {
		case 2:
			result.Delay = time.Second
		case 3:
			result.Delay = 2 * time.Second
		case 4:
			result.Delay = 4 * time.Second
		}
	}
	return result
}
func (limiter *Limiter) expire(now time.Time) {
	for element := limiter.lru.Back(); element != nil; {
		previous := element.Prev()
		entry, ok := element.Value.(*limitEntry)
		if !ok {
			panic("invalid limiter entry")
		}
		if now.Sub(entry.touched) > time.Hour {
			delete(limiter.entries, entry.key)
			limiter.lru.Remove(element)
		}
		element = previous
	}
}

// UsernameKey canonicalizes a submitted username for a limiter key.
func UsernameKey(value string) (string, error) {
	normalized, err := identity.Username(value)
	if err != nil {
		return "", err
	}
	return "username:" + normalized, nil
}

// TokenKey bounds raw bearer input and returns its SHA-256 digest key.
func TokenKey(value string) (string, error) {
	if len(value) > 128 {
		return "", fmt.Errorf("token limiter value too long")
	}
	digest := sha256.Sum256([]byte(value))
	return "token:" + fmt.Sprintf("%x", digest), nil
}
func AccountKey(value string) (string, error) {
	if len(value) == 0 || len(value) > 128 {
		return "", fmt.Errorf("account limiter value invalid")
	}
	return "account:" + value, nil
}

func rateLimitMiddleware(limiter *Limiter, resolver *ClientIPResolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !isAPIPath(c.Request().URL.Path) {
				return next(c)
			}
			result := limiter.Check(LimitUnauthenticatedAPI, resolver.Resolve(c.Request()), time.Now())
			if !result.Allowed {
				seconds := max(1, int(result.RetryAfter.Seconds()+0.999))
				c.Response().Header().Set("Retry-After", strconv.Itoa(seconds))
				return NewError(CodeRateLimited)
			}
			return next(c)
		}
	}
}
