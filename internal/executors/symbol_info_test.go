package executors

import (
	"testing"

	"github.com/adshao/go-binance/v2/common"
)

// btcFilters mirrors BTCUSDT: tickSize 0.10 with a 2-decimal price field.
// btcFilters 模拟 BTCUSDT：tickSize 为 0.10，价格字段为 2 位小数
//
// This is the combination the old fmt.Sprintf("%.2f", price) got wrong: "%.2f"
// happily produces 60123.45, which is not a multiple of 0.1 and is rejected by
// PRICE_FILTER.
// 这正是旧代码 fmt.Sprintf("%.2f", price) 出错的组合：
// "%.2f" 会产生 60123.45，它不是 0.1 的整数倍，会被 PRICE_FILTER 拒绝。
func btcFilters() *SymbolFilters {
	return &SymbolFilters{
		Symbol:        "BTCUSDT",
		TickSize:      0.1,
		StepSize:      0.001,
		MinQty:        0.001,
		MaxQty:        1000,
		MinNotional:   100,
		priceDecimals: 1,
		qtyDecimals:   3,
	}
}

// dogeFilters mirrors DOGEUSDT: whole-coin quantities, 5-decimal prices.
// dogeFilters 模拟 DOGEUSDT：数量为整数，价格 5 位小数
func dogeFilters() *SymbolFilters {
	return &SymbolFilters{
		Symbol:        "DOGEUSDT",
		TickSize:      0.00001,
		StepSize:      1,
		MinQty:        1,
		MaxQty:        5000000,
		MinNotional:   5,
		priceDecimals: 5,
		qtyDecimals:   0,
	}
}

func TestFormatQuantityMatchesStepSize(t *testing.T) {
	tests := []struct {
		name    string
		filters *SymbolFilters
		qty     float64
		want    string
	}{
		// Previously fmt.Sprintf("%.4f", 1234.0) produced "1234.0000", which has
		// more decimals than DOGEUSDT allows and is rejected with -1111.
		// 之前 fmt.Sprintf("%.4f", 1234.0) 会产生 "1234.0000"，
		// 小数位超过 DOGEUSDT 允许的精度，会被 -1111 拒绝。
		{"doge whole coins", dogeFilters(), 1234.0, "1234"},
		{"doge floors partial coin", dogeFilters(), 1234.987, "1234"},
		{"btc three decimals", btcFilters(), 0.0123456, "0.012"},
		{"btc exact step", btcFilters(), 0.005, "0.005"},
		// Floor, never round up: closing 0.0019 BTC must not try to sell 0.002.
		// 只向下取整，绝不向上：平掉 0.0019 BTC 不能变成卖出 0.002。
		{"btc never rounds up", btcFilters(), 0.0019, "0.001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.FormatQuantity(tt.qty); got != tt.want {
				t.Errorf("FormatQuantity(%v) = %q, want %q", tt.qty, got, tt.want)
			}
		})
	}
}

func TestFormatPriceSnapsToTickSize(t *testing.T) {
	btc := btcFilters()

	// 60123.45 is not a multiple of 0.1 and would be rejected outright.
	// 60123.45 不是 0.1 的整数倍，会被直接拒单。
	if got := btc.FormatPrice(60123.45, RoundNearest); got != "60123.5" {
		t.Errorf("FormatPrice(60123.45) = %q, want %q", got, "60123.5")
	}
	if got := btc.FormatPrice(60123.45, RoundDown); got != "60123.4" {
		t.Errorf("FormatPrice(60123.45, RoundDown) = %q, want %q", got, "60123.4")
	}
	if got := btc.FormatPrice(60123.41, RoundUp); got != "60123.5" {
		t.Errorf("FormatPrice(60123.41, RoundUp) = %q, want %q", got, "60123.5")
	}

	// The old "%.2f" turned 0.12345 into "0.12" — a 2.8% shift of the stop level.
	// 旧的 "%.2f" 会把 0.12345 变成 "0.12" —— 止损位偏移 2.8%。
	doge := dogeFilters()
	if got := doge.FormatPrice(0.12345, RoundNearest); got != "0.12345" {
		t.Errorf("FormatPrice(0.12345) = %q, want %q", got, "0.12345")
	}
}

func TestFormatStopPriceRoundsAwayFromMarket(t *testing.T) {
	btc := btcFilters()

	// A long's stop sits below market, so tick alignment must round down and
	// never drift up across the current price.
	// 多仓止损位于市价下方，因此对齐时必须向下取整，绝不能向上越过当前价。
	if got := btc.FormatStopPrice(60123.49, "long"); got != "60123.4" {
		t.Errorf("long stop = %q, want %q", got, "60123.4")
	}

	// A short's stop sits above market, so it must round up.
	// 空仓止损位于市价上方，因此必须向上取整。
	if got := btc.FormatStopPrice(60123.41, "short"); got != "60123.5" {
		t.Errorf("short stop = %q, want %q", got, "60123.5")
	}
}

func TestValidateQuantity(t *testing.T) {
	btc := btcFilters()

	if err := btc.ValidateQuantity(0.0005); err == nil {
		t.Error("expected below-minimum quantity to be rejected")
	}
	if err := btc.ValidateQuantity(2000); err == nil {
		t.Error("expected above-maximum quantity to be rejected")
	}
	if err := btc.ValidateQuantity(0.01); err != nil {
		t.Errorf("expected valid quantity to pass, got %v", err)
	}
}

func TestDecimalsOf(t *testing.T) {
	tests := []struct {
		size     float64
		fallback int
		want     int
	}{
		// Binance reports "0.00100000"; that means 3 decimals, not 8.
		// 币安返回 "0.00100000"，代表 3 位小数而非 8 位。
		{0.001, 8, 3},
		{0.1, 8, 1},
		{1, 8, 0},
		{0.00001, 8, 5},
		{0, 4, 4}, // missing filter falls back to the precision field / filter 缺失时回退到 precision 字段
	}

	for _, tt := range tests {
		if got := decimalsOf(tt.size, tt.fallback); got != tt.want {
			t.Errorf("decimalsOf(%v, %d) = %d, want %d", tt.size, tt.fallback, got, tt.want)
		}
	}
}

func TestNormalizeSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"BTC/USDT": "BTCUSDT",
		"BTCUSDT":  "BTCUSDT",
		"btc/usdt": "BTCUSDT",
	} {
		if got := normalizeSymbol(in); got != want {
			t.Errorf("normalizeSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsRetryableBinanceError(t *testing.T) {
	// A deterministic rejection must not be retried: the old code burned 14
	// seconds re-sending a request Binance would refuse identically every time.
	// 确定性拒绝不应重试：旧代码会花 14 秒反复发送一个币安每次都会以同样理由拒绝的请求。
	if isRetryableBinanceError(apiError(-1111, "Precision is over the maximum")) {
		t.Error("-1111 (precision) should not be retried")
	}
	if isRetryableBinanceError(apiError(-2019, "Margin is insufficient")) {
		t.Error("-2019 (insufficient margin) should not be retried")
	}
	if isRetryableBinanceError(apiError(-4120, "STOP_ORDER_SWITCH_ALGO")) {
		t.Error("-4120 (algo migration) should not be retried")
	}

	if !isRetryableBinanceError(apiError(-1003, "Too many requests")) {
		t.Error("-1003 (rate limit) should be retried")
	}
	if !isRetryableBinanceError(apiError(-1021, "Timestamp for this request...")) {
		t.Error("-1021 (clock drift) should be retried")
	}

	// Network/transport failures carry no API code and are worth another try.
	// 网络/传输故障没有 API 错误码，值得重试。
	if !isRetryableBinanceError(errTransport{}) {
		t.Error("transport errors should be retried")
	}
	if isRetryableBinanceError(nil) {
		t.Error("nil should not be retried")
	}
}

type errTransport struct{}

func (errTransport) Error() string { return "connection reset by peer" }

// apiError builds a Binance API error wrapped the way callAPI returns it.
// apiError 构造一个与 callAPI 返回形式一致的币安 API 错误
func apiError(code int64, msg string) error {
	return &common.APIError{Code: code, Message: msg}
}
