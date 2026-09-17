package dataflows

import (
	"testing"
	"time"
)

// TestKlineWindowNeverExceedsAPILimit guards the fix for stale OHLCV data.
// TestKlineWindowNeverExceedsAPILimit 守护 OHLCV 数据过期问题的修复
//
// Binance returns the OLDEST 1000 candles from startTime, so any window wider
// than 1000 candles used to silently truncate before the present: 3m over 3
// days asked for 1440 candles and the response ended ~22 hours in the past.
// 币安从 startTime 起返回「最早」的 1000 根 K 线，因此任何超过 1000 根的窗口
// 之前都会在到达当前时间之前被悄悄截断：
// 3m 周期回看 3 天需要 1440 根，响应会停在约 22 小时之前。
func TestKlineWindowNeverExceedsAPILimit(t *testing.T) {
	tests := []struct {
		name         string
		timeframe    string
		lookbackDays int
		wantClamped  bool
	}{
		// The configuration the README recommends, and the one that was broken.
		// README 推荐的配置，也正是此前出问题的那一组。
		{"3m over 3 days needs 1440 candles", "3m", 3, true},
		{"1m over 1 day needs 1440 candles", "1m", 1, true},
		{"15m over 5 days needs 480 candles", "15m", 5, false},
		{"1h over 10 days needs 240 candles", "1h", 10, false},
		{"4h over 15 days needs 90 candles", "4h", 15, false},
		{"1d over 60 days needs 60 candles", "1d", 60, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := convertTimeframe(tt.timeframe)
			intervalDur := intervalDuration(interval)
			if intervalDur <= 0 {
				t.Fatalf("intervalDuration(%q) = %v, want a positive duration", interval, intervalDur)
			}

			endTime := time.Now()
			startTime := endTime.AddDate(0, 0, -tt.lookbackDays)
			requested := endTime.Sub(startTime)

			// Mirror the clamp applied in GetOHLCV
			// 复现 GetOHLCV 中的收敛逻辑
			maxSpan := time.Duration(binanceKlineLimit) * intervalDur
			clamped := requested > maxSpan
			if clamped {
				startTime = endTime.Add(-maxSpan)
			}

			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v (requested %v, max %v)",
					clamped, tt.wantClamped, requested, maxSpan)
			}

			// The whole point: the window must always fit in one API response,
			// so the last candle returned is the current one.
			// 关键点：窗口必须始终能装进一次 API 响应，
			// 这样返回的最后一根 K 线才是当前这根。
			candles := endTime.Sub(startTime) / intervalDur
			if candles > binanceKlineLimit {
				t.Errorf("window spans %d candles, exceeds the %d limit", candles, binanceKlineLimit)
			}
		})
	}
}

// TestIntervalDurationCoversSupportedTimeframes checks the clamp is applied to
// every interval convertTimeframe can emit.
// TestIntervalDurationCoversSupportedTimeframes 检查 convertTimeframe 可能产生的
// 每一个时间周期都能被收敛逻辑覆盖
//
// A timeframe missing here would return 0 and silently skip the clamp,
// reintroducing the stale-data bug for that interval.
// 此处遗漏的时间周期会返回 0 并跳过收敛逻辑，
// 使该周期重新出现数据过期的问题。
func TestIntervalDurationCoversSupportedTimeframes(t *testing.T) {
	supported := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}

	for _, tf := range supported {
		if got := intervalDuration(tf); got <= 0 {
			t.Errorf("intervalDuration(%q) = %v, want a positive duration", tf, got)
		}
	}

	// "1M" is calendar-based; returning 0 deliberately skips the clamp.
	// "1M" 按自然月计算；返回 0 是刻意跳过收敛逻辑。
	if got := intervalDuration("1M"); got != 0 {
		t.Errorf("intervalDuration(\"1M\") = %v, want 0", got)
	}
}
