package httpserver

import (
	"testing"
	"time"
)

func TestLimiterExhaustionRollingWindowAndEviction(t *testing.T) {
	limiter := NewLimiter(2)
	now := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		if !limiter.Check(LimitRecoveryIdentifier, "one", now).Allowed {
			t.Fatal("initial attempt rejected")
		}
	}
	if limiter.Check(LimitRecoveryIdentifier, "one", now).Allowed {
		t.Fatal("fourth attempt accepted")
	}
	if !limiter.Check(LimitRecoveryIdentifier, "two", now).Allowed || !limiter.Check(LimitRecoveryIdentifier, "three", now).Allowed {
		t.Fatal("new keys rejected")
	}
	if !limiter.Check(LimitRecoveryIdentifier, "one", now).Allowed {
		t.Fatal("least-recently-used key was not evicted")
	}
}

func TestLimiterFailureDefinitionsAndProgressiveDelay(t *testing.T) {
	limiter := NewLimiter(10)
	now := time.Now()
	if !limiter.Check(LimitLoginUsername, "username:ada", now).Allowed {
		t.Fatal("failure definition blocked before a failure")
	}
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now); result.Delay != 0 {
		t.Fatalf("first delay = %v", result.Delay)
	}
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now); result.Delay != time.Second {
		t.Fatalf("second delay = %v", result.Delay)
	}
	limiter.RecordFailure(LimitLoginUsername, "username:ada", now)
	limiter.RecordFailure(LimitLoginUsername, "username:ada", now)
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("fifth result = %+v", result)
	}
}

func TestLimiterRefreshesActiveRollingEntryBeforeExpirySweep(t *testing.T) {
	limiter := NewLimiter(3)
	start := time.Now()
	limiter.RecordFailure(LimitLoginUsername, "username:ada", start)
	limiter.RecordFailure(LimitLoginUsername, "username:ada", start.Add(2*time.Hour))
	limiter.Check(LimitRecoveryIP, "ip:one", start.Add(2*time.Hour))
	limiter.Check(LimitRecoveryIdentifier, "identifier:one", start.Add(2*time.Hour))
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", start.Add(2*time.Hour)); result.Delay != time.Second {
		t.Fatalf("active entry was expired: %+v", result)
	}
}
