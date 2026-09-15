package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
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
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now.Add(time.Second)); result.Delay != 2*time.Second {
		t.Fatalf("third delay = %v", result.Delay)
	}
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now.Add(3*time.Second)); result.Delay != 4*time.Second {
		t.Fatalf("fourth delay = %v", result.Delay)
	}
	if result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now.Add(7*time.Second)); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("fifth result = %+v", result)
	}
}

func TestLimiterBlocksDuringProgressiveBackoff(t *testing.T) {
	limiter := NewLimiter(10)
	now := time.Now()
	limiter.RecordFailure(LimitLoginUsername, "username:ada", now)
	result := limiter.RecordFailure(LimitLoginUsername, "username:ada", now)
	if result.Delay != time.Second {
		t.Fatalf("backoff delay = %v", result.Delay)
	}
	if result = limiter.Check(LimitLoginUsername, "username:ada", now.Add(500*time.Millisecond)); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("active backoff result = %+v", result)
	}
	if result = limiter.Check(LimitLoginUsername, "username:ada", now.Add(time.Second)); !result.Allowed {
		t.Fatalf("expired backoff result = %+v", result)
	}
}

func TestLoginIPFailureLimitIsLayered(t *testing.T) {
	limiter := NewLimiter(10)
	now := time.Now()
	for attempt := 0; attempt < 29; attempt++ {
		if result := limiter.RecordFailure(LimitLoginIP, "198.51.100.10", now); !result.Allowed {
			t.Fatalf("IP failure %d blocked early: %+v", attempt, result)
		}
	}
	if result := limiter.RecordFailure(LimitLoginIP, "198.51.100.10", now); result.Allowed || result.RetryAfter <= 0 {
		t.Fatalf("IP limit result = %+v", result)
	}
}

func TestLoginDimensionsReturnLongestBlockedRetry(t *testing.T) {
	limiter := NewLimiter(10)
	now := time.Now()
	for range 30 {
		limiter.RecordFailure(LimitLoginIP, "198.51.100.10", now.Add(-10*time.Minute))
	}
	username := limiter.entry(LimitLoginUsername, limitRegistry[LimitLoginUsername], "username:ada", now)
	username.events = []time.Time{now.Add(-2 * time.Minute), now.Add(-90 * time.Second), now.Add(-60 * time.Second), now.Add(-30 * time.Second), now}
	ipResult := limiter.Check(LimitLoginIP, "198.51.100.10", now)
	usernameResult := limiter.Check(LimitLoginUsername, "username:ada", now)
	if ipResult.RetryAfter <= 0 || usernameResult.RetryAfter <= ipResult.RetryAfter {
		t.Fatalf("individual retries IP=%v username=%v", ipResult.RetryAfter, usernameResult.RetryAfter)
	}
	resolver, err := NewClientIPResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://mia.test/api/v1/auth/login", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	context := echo.New().NewContext(request, httptest.NewRecorder())
	result := (&Server{limiter: limiter, resolver: resolver}).CheckLogin(context, "ada", false)
	if result.Allowed || result.RetryAfter < usernameResult.RetryAfter-time.Second {
		t.Fatalf("combined login result = %+v", result)
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

func TestMentorTargetLimitsUseFixedIndependentAccountAndIPBudgets(t *testing.T) {
	limiter := NewLimiter(50_000)
	now := time.Now()
	for attempt := 0; attempt < 10; attempt++ {
		if result := limiter.Check(LimitMentorTargetAccount, "account:supervisor", now); !result.Allowed {
			t.Fatalf("account attempt %d blocked early: %+v", attempt+1, result)
		}
	}
	if result := limiter.Check(LimitMentorTargetAccount, "account:supervisor", now); result.Allowed {
		t.Fatal("eleventh account attempt accepted")
	}
	for attempt := 0; attempt < 30; attempt++ {
		if result := limiter.Check(LimitMentorTargetIP, "198.51.100.20", now); !result.Allowed {
			t.Fatalf("IP attempt %d blocked early: %+v", attempt+1, result)
		}
	}
	if result := limiter.Check(LimitMentorTargetIP, "198.51.100.20", now); result.Allowed {
		t.Fatal("thirty-first IP attempt accepted")
	}
	if result := limiter.Check(LimitMentorTargetAccount, "account:other", now); !result.Allowed {
		t.Fatal("independent account was blocked")
	}
}
