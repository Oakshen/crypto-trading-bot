package executors

import (
	"sync"
	"time"
)

// DailyOpenPositionLimiter limits how many successful opening trades (BUY/SELL) can happen per day.
// DailyOpenPositionLimiter 用于限制单日成功开仓（BUY/SELL）的次数。
type DailyOpenPositionLimiter struct {
	maxPerDay int

	mu        sync.Mutex
	dayKey    string
	remaining int
	now       func() time.Time
}

func NewDailyOpenPositionLimiter(maxPerDay int) *DailyOpenPositionLimiter {
	return newDailyOpenPositionLimiter(maxPerDay, time.Now)
}

func newDailyOpenPositionLimiter(maxPerDay int, now func() time.Time) *DailyOpenPositionLimiter {
	if now == nil {
		now = time.Now
	}
	return &DailyOpenPositionLimiter{
		maxPerDay: maxPerDay,
		now:       now,
	}
}

// Acquire reserves one opening slot for today.
// Acquire 预留一次今日开仓额度。
//
// If the trade later fails, call release() to roll back the reservation.
// 如果交易最终失败，请调用 release() 回滚本次预留。
func (l *DailyOpenPositionLimiter) Acquire() (release func(), ok bool, remaining int) {
	if l == nil || l.maxPerDay <= 0 {
		return nil, true, -1
	}

	l.mu.Lock()
	now := l.now()
	l.resetIfNeededLocked(now)
	if l.remaining <= 0 {
		l.mu.Unlock()
		return nil, false, 0
	}
	l.remaining--
	remainingAfterReserve := l.remaining
	acquiredDay := l.dayKey
	l.mu.Unlock()

	var once sync.Once
	release = func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			// If day changed, reset happens naturally; never add beyond max.
			// 如果日期已变化，将自然重置；不要让额度超过 max。
			currentNow := l.now()
			currentDay := currentNow.Format("2006-01-02")
			if currentDay != acquiredDay {
				l.resetIfNeededLocked(currentNow)
				return
			}
			if l.remaining < l.maxPerDay {
				l.remaining++
			}
		})
	}

	return release, true, remainingAfterReserve
}

func (l *DailyOpenPositionLimiter) resetIfNeededLocked(now time.Time) {
	today := now.Format("2006-01-02")
	if l.dayKey == "" || l.dayKey != today {
		l.dayKey = today
		l.remaining = l.maxPerDay
	}
}
