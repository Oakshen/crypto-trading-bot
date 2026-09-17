package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGetBalanceHistoryWindow guards the timezone fix: the query must select
// exactly the requested window regardless of the host's UTC offset.
// TestGetBalanceHistoryWindow 守护时区修复：无论主机的 UTC 偏移为何，
// 查询都必须精确返回所请求的时间窗口。
//
// SQLite's datetime('now') is UTC while timestamps are written as local time,
// so the previous query shifted the window by the local offset — on a UTC-7
// host the 1h range returned nothing at all.
// SQLite 的 datetime('now') 为 UTC，而 timestamp 按本地时间写入，
// 因此旧查询会把窗口整体平移一个本地偏移量——在 UTC-7 的主机上，
// 1 小时范围完全查不到数据。
func TestGetBalanceHistoryWindow(t *testing.T) {
	db, err := NewStorage(filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	defer db.Close()

	now := time.Now()
	// One sample every 10 minutes for 48 hours
	// 48 小时内每 10 分钟一个采样点
	for i := 0; i <= 288; i++ {
		if err := db.SaveBalanceHistory(&BalanceHistory{
			Timestamp:        now.Add(-time.Duration(i) * 10 * time.Minute),
			TotalBalance:     10000,
			AvailableBalance: 7000,
		}); err != nil {
			t.Fatalf("SaveBalanceHistory: %v", err)
		}
	}

	tests := []struct {
		hours   int
		wantMin int // 至少这么多点 / at least this many points
	}{
		{1, 6},    // 6 samples in the last hour / 最近 1 小时 6 个点
		{3, 18},   // 18 in three hours / 3 小时 18 个点
		{24, 144}, // 144 in a day / 1 天 144 个点
	}

	for _, tt := range tests {
		history, err := db.GetBalanceHistory(tt.hours)
		if err != nil {
			t.Fatalf("GetBalanceHistory(%d): %v", tt.hours, err)
		}

		if len(history) < tt.wantMin {
			t.Errorf("GetBalanceHistory(%d) returned %d points, want at least %d",
				tt.hours, len(history), tt.wantMin)
		}

		// Nothing older than the window may leak in
		// 窗口之外的旧数据不得混入
		cutoff := now.Add(-time.Duration(tt.hours) * time.Hour)
		for _, h := range history {
			if h.Timestamp.Before(cutoff.Add(-time.Minute)) {
				t.Errorf("GetBalanceHistory(%d) returned %v, older than cutoff %v",
					tt.hours, h.Timestamp, cutoff)
				break
			}
		}
	}

	_ = os.Stdout
}
