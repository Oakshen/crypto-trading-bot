package web

import "testing"

// TestExtractReason guards the decision tape's rationale column.
// TestExtractReason 守护决策带的理由列
func TestExtractReason(t *testing.T) {
	decision := `【BTC/USDT】
**交易方向**: BUY
**入场理由**: ADX 41.01 显示强上升趋势，回调至 EMA20 获得支撑。
【ETH/USDT】
**交易方向**: HOLD
**入场理由**: 4h 级别仍在震荡区间，等待突破确认。`

	// A multi-symbol decision must not attribute one pair's rationale to another
	// 多币种决策中不得张冠李戴
	if got := extractReason(decision, "BTC/USDT"); got != "ADX 41.01 显示强上升趋势，回调至 EMA20 获得支撑。" {
		t.Errorf("BTC reason = %q", got)
	}
	if got := extractReason(decision, "ETH/USDT"); got != "4h 级别仍在震荡区间，等待突破确认。" {
		t.Errorf("ETH reason = %q", got)
	}
	if got := extractReason("", "BTC/USDT"); got != "" {
		t.Errorf("empty decision should yield empty reason, got %q", got)
	}
	if got := extractReason("交易方向: HOLD", "BTC/USDT"); got != "" {
		t.Errorf("decision without a reason should yield empty, got %q", got)
	}
}

func TestDisplaySymbol(t *testing.T) {
	for in, want := range map[string]string{
		"BTCUSDT":  "BTC/USDT",
		"BTC/USDT": "BTC/USDT",
		"SOLUSDT":  "SOL/USDT",
		"WEIRD":    "WEIRD",
	} {
		if got := displaySymbol(in); got != want {
			t.Errorf("displaySymbol(%q) = %q, want %q", in, got, want)
		}
	}
}
