package executors

import (
	"testing"
	"time"
)

func TestDailyOpenPositionLimiter_Unlimited(t *testing.T) {
	limiter := NewDailyOpenPositionLimiter(0)

	release, ok, remaining := limiter.Acquire()
	if !ok {
		t.Fatalf("expected ok=true for unlimited limiter")
	}
	if release != nil {
		t.Fatalf("expected release=nil for unlimited limiter")
	}
	if remaining != -1 {
		t.Fatalf("expected remaining=-1 for unlimited limiter, got %d", remaining)
	}
}

func TestDailyOpenPositionLimiter_AcquireAndRelease(t *testing.T) {
	now := time.Date(2026, 1, 3, 12, 0, 0, 0, time.Local)
	limiter := newDailyOpenPositionLimiter(2, func() time.Time { return now })

	release1, ok, remaining := limiter.Acquire()
	if !ok || remaining != 1 || release1 == nil {
		t.Fatalf("first acquire: ok=%v remaining=%d releaseNil=%v", ok, remaining, release1 == nil)
	}

	release2, ok, remaining := limiter.Acquire()
	if !ok || remaining != 0 || release2 == nil {
		t.Fatalf("second acquire: ok=%v remaining=%d releaseNil=%v", ok, remaining, release2 == nil)
	}

	_, ok, remaining = limiter.Acquire()
	if ok || remaining != 0 {
		t.Fatalf("third acquire should fail: ok=%v remaining=%d", ok, remaining)
	}

	// Rollback one reservation.
	release2()

	release3, ok, remaining := limiter.Acquire()
	if !ok || remaining != 0 || release3 == nil {
		t.Fatalf("acquire after release should succeed: ok=%v remaining=%d releaseNil=%v", ok, remaining, release3 == nil)
	}

	// Cleanup: rollback remaining reservations.
	release1()
	release3()

	release4, ok, remaining := limiter.Acquire()
	if !ok || remaining != 1 || release4 == nil {
		t.Fatalf("after full rollback, remaining should reset to 1: ok=%v remaining=%d releaseNil=%v", ok, remaining, release4 == nil)
	}
}

func TestDailyOpenPositionLimiter_DailyReset(t *testing.T) {
	now := time.Date(2026, 1, 3, 23, 59, 0, 0, time.Local)
	limiter := newDailyOpenPositionLimiter(1, func() time.Time { return now })

	_, ok, remaining := limiter.Acquire()
	if !ok || remaining != 0 {
		t.Fatalf("day1 acquire: ok=%v remaining=%d", ok, remaining)
	}

	_, ok, remaining = limiter.Acquire()
	if ok || remaining != 0 {
		t.Fatalf("day1 second acquire should fail: ok=%v remaining=%d", ok, remaining)
	}

	// Move to next day; should reset.
	now = time.Date(2026, 1, 4, 0, 1, 0, 0, time.Local)
	_, ok, remaining = limiter.Acquire()
	if !ok || remaining != 0 {
		t.Fatalf("day2 acquire after reset: ok=%v remaining=%d", ok, remaining)
	}
}

func TestDailyOpenPositionLimiter_ReleaseAcrossMidnightDoesNotIncreaseBeyondMax(t *testing.T) {
	now := time.Date(2026, 1, 3, 23, 59, 0, 0, time.Local)
	limiter := newDailyOpenPositionLimiter(1, func() time.Time { return now })

	release, ok, remaining := limiter.Acquire()
	if !ok || remaining != 0 || release == nil {
		t.Fatalf("day1 acquire: ok=%v remaining=%d releaseNil=%v", ok, remaining, release == nil)
	}

	// Fail after midnight, rollback should not grant extra quota for the new day.
	now = time.Date(2026, 1, 4, 0, 1, 0, 0, time.Local)
	release()

	_, ok, remaining = limiter.Acquire()
	if !ok || remaining != 0 {
		t.Fatalf("day2 acquire: ok=%v remaining=%d", ok, remaining)
	}

	_, ok, remaining = limiter.Acquire()
	if ok || remaining != 0 {
		t.Fatalf("day2 second acquire should fail: ok=%v remaining=%d", ok, remaining)
	}
}
