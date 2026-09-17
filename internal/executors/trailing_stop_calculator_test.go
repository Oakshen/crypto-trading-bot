package executors

import (
	"math"
	"testing"
)

func TestCalculateInitialStop(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	tests := []struct {
		name       string
		symbol     string
		entryPrice float64
		atr        float64
		side       string
	}{
		{
			name:       "Long position initial stop",
			symbol:     "BTCUSDT",
			entryPrice: 50000,
			atr:        500,
			side:       "long",
		},
		{
			name:       "Short position initial stop",
			symbol:     "BTCUSDT",
			entryPrice: 50000,
			atr:        500,
			side:       "short",
		},
		{
			name:       "ETH long position initial stop",
			symbol:     "ETHUSDT",
			entryPrice: 3000,
			atr:        50,
			side:       "long",
		},
		{
			name:       "SOL short position with higher volatility",
			symbol:     "SOLUSDT",
			entryPrice: 100,
			atr:        5,
			side:       "short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			distance := calc.GetConfig(tt.symbol).InitialATRMultiplier * tt.atr

			// A long stops below entry, a short stops above it
			// 多仓止损在开仓价下方，空仓止损在开仓价上方
			expected := tt.entryPrice - distance
			if tt.side == "short" {
				expected = tt.entryPrice + distance
			}

			result := calc.CalculateInitialStop(tt.symbol, tt.entryPrice, tt.atr, tt.side)
			if math.Abs(result-expected) > 0.01 {
				t.Errorf("CalculateInitialStop() = %.2f, expected %.2f", result, expected)
			}
		})
	}
}

func TestCalculateTrailingStop(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	// Expectations are derived from each symbol's configured multiplier rather
	// than hardcoded, so retuning risk parameters cannot silently invalidate them.
	// 预期值由各交易对配置的倍数推导而来，而非写死，
	// 这样重新调整风险参数时不会悄悄让断言失效。
	tests := []struct {
		name         string
		symbol       string
		highestPrice float64
		atr          float64
		side         string
	}{
		{
			name:         "Long position trailing stop",
			symbol:       "BTCUSDT",
			highestPrice: 52000,
			atr:          500,
			side:         "long",
		},
		{
			name:         "Short position trailing stop",
			symbol:       "BTCUSDT",
			highestPrice: 48000, // This is actually lowest price for short
			atr:          500,
			side:         "short",
		},
		{
			name:         "ETH long with small ATR",
			symbol:       "ETHUSDT",
			highestPrice: 3200,
			atr:          40,
			side:         "long",
		},
		{
			name:         "SOL short",
			symbol:       "SOLUSDT",
			highestPrice: 95, // lowest price
			atr:          5,
			side:         "short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			distance := calc.GetConfig(tt.symbol).TrailingATRMultiplier * tt.atr

			// A long trails below the high, a short trails above the low
			// 多仓止损跟在最高价下方，空仓止损跟在最低价上方
			expected := tt.highestPrice - distance
			if tt.side == "short" {
				expected = tt.highestPrice + distance
			}

			result := calc.CalculateTrailingStop(tt.symbol, tt.highestPrice, tt.atr, tt.side)
			if math.Abs(result-expected) > 0.01 {
				t.Errorf("CalculateTrailingStop() = %.2f, expected %.2f", result, expected)
			}
		})
	}
}

func TestIsValidUpdate(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	tests := []struct {
		name        string
		side        string
		oldStopLoss float64
		newStopLoss float64
		expected    bool
	}{
		{
			name:        "Long position - stop moves up (valid)",
			side:        "long",
			oldStopLoss: 48000,
			newStopLoss: 49000,
			expected:    true,
		},
		{
			name:        "Long position - stop moves down (invalid)",
			side:        "long",
			oldStopLoss: 49000,
			newStopLoss: 48000,
			expected:    false,
		},
		{
			name:        "Short position - stop moves down (valid)",
			side:        "short",
			oldStopLoss: 52000,
			newStopLoss: 51000,
			expected:    true,
		},
		{
			name:        "Short position - stop moves up (invalid)",
			side:        "short",
			oldStopLoss: 51000,
			newStopLoss: 52000,
			expected:    false,
		},
		{
			name:        "Long position - no change",
			side:        "long",
			oldStopLoss: 50000,
			newStopLoss: 50000,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.IsValidUpdate(tt.side, tt.oldStopLoss, tt.newStopLoss)
			if result != tt.expected {
				t.Errorf("IsValidUpdate() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestShouldUpdate(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	// The move is expressed as a multiple of the symbol's own threshold, so the
	// test stays correct whatever the configured threshold is.
	// 变动幅度以各交易对自身阈值的倍数表示，
	// 因此无论阈值配置为何，本测试都保持正确。
	tests := []struct {
		name          string
		symbol        string
		oldStopLoss   float64
		thresholdMult float64 // 相对阈值的倍数 / Multiple of the configured threshold
		expected      bool
	}{
		{
			name:          "BTC - change exceeds threshold",
			symbol:        "BTCUSDT",
			oldStopLoss:   50000,
			thresholdMult: 2.0,
			expected:      true,
		},
		{
			name:          "BTC - change below threshold",
			symbol:        "BTCUSDT",
			oldStopLoss:   50000,
			thresholdMult: 0.5,
			expected:      false,
		},
		{
			name:          "SOL - change exceeds threshold",
			symbol:        "SOLUSDT",
			oldStopLoss:   100,
			thresholdMult: 2.0,
			expected:      true,
		},
		{
			name:          "SOL - change below threshold",
			symbol:        "SOLUSDT",
			oldStopLoss:   100,
			thresholdMult: 0.5,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold := calc.GetConfig(tt.symbol).UpdateThreshold
			changePercent := threshold * tt.thresholdMult
			newStopLoss := tt.oldStopLoss * (1 + changePercent/100)

			result := calc.ShouldUpdate(tt.symbol, tt.oldStopLoss, newStopLoss)
			if result != tt.expected {
				t.Errorf("ShouldUpdate(%.4f -> %.4f, %.2f%% vs threshold %.2f%%) = %v, expected %v",
					tt.oldStopLoss, newStopLoss, changePercent, threshold, result, tt.expected)
			}
		})
	}
}

func TestValidateStopDistance(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	tests := []struct {
		name       string
		symbol     string
		entryPrice float64
		stopPrice  float64
		side       string
		expected   bool
	}{
		{
			name:       "BTC long - valid distance (2%)",
			symbol:     "BTCUSDT",
			entryPrice: 50000,
			stopPrice:  49000, // 2% below
			side:       "long",
			expected:   true,
		},
		{
			name:       "BTC long - too tight (0.3%)",
			symbol:     "BTCUSDT",
			entryPrice: 50000,
			stopPrice:  49850, // 0.3% below (below 0.5% min)
			side:       "long",
			expected:   false,
		},
		{
			name:       "BTC long - too wide (7%)",
			symbol:     "BTCUSDT",
			entryPrice: 50000,
			stopPrice:  46500, // 7% below (above 6% max for BTC)
			side:       "long",
			expected:   false,
		},
		{
			name:       "ETH short - valid distance (3%)",
			symbol:     "ETHUSDT",
			entryPrice: 3000,
			stopPrice:  3090, // 3% above
			side:       "short",
			expected:   true,
		},
		{
			name:       "SOL long - valid wider range (5%)",
			symbol:     "SOLUSDT",
			entryPrice: 100,
			stopPrice:  95, // 5% below (within 0.5%-8% for SOL)
			side:       "long",
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.ValidateStopDistance(tt.symbol, tt.entryPrice, tt.stopPrice, tt.side)
			if result != tt.expected {
				t.Errorf("ValidateStopDistance() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGetConfig(t *testing.T) {
	calc := NewTrailingStopCalculator(nil)

	tests := []struct {
		name                       string
		symbol                     string
		expectedTrailingMultiplier float64
	}{
		// These track the per-symbol multipliers in trailing_stop_calculator.go.
		// 这些值对应 trailing_stop_calculator.go 中各交易对的倍数配置。
		{
			name:                       "BTC config",
			symbol:                     "BTCUSDT",
			expectedTrailingMultiplier: 2.8,
		},
		{
			name:                       "ETH config",
			symbol:                     "ETHUSDT",
			expectedTrailingMultiplier: 2.7,
		},
		{
			name:                       "SOL config - wider multiplier",
			symbol:                     "SOLUSDT",
			expectedTrailingMultiplier: 2.5,
		},
		{
			name:                       "Unknown symbol - uses default",
			symbol:                     "XYZUSDT",
			expectedTrailingMultiplier: 3.0, // DEFAULT config
		},
		{
			name:                       "Symbol with slash",
			symbol:                     "BTC/USDT",
			expectedTrailingMultiplier: 2.8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := calc.GetConfig(tt.symbol)
			if config.TrailingATRMultiplier != tt.expectedTrailingMultiplier {
				t.Errorf("GetConfig().TrailingATRMultiplier = %.1f, expected %.1f",
					config.TrailingATRMultiplier, tt.expectedTrailingMultiplier)
			}
		})
	}
}

func TestTrailingStopScenario(t *testing.T) {
	// Integration test: simulate a complete trailing stop scenario
	// 集成测试：模拟一个完整的追踪止损场景
	calc := NewTrailingStopCalculator(nil)

	// Scenario: BTC long position
	// 场景：BTC 多仓
	symbol := "BTCUSDT"
	entryPrice := 50000.0
	atr := 500.0

	// Derive the expectations from the configured multipliers instead of
	// hardcoding them, so tuning risk parameters does not silently break this
	// test the way it did before.
	// 直接从配置的倍数推导预期值，而不是写死数字，
	// 这样调整风险参数时就不会像之前那样悄悄使本测试失效。
	cfg := calc.GetConfig(symbol)

	// 1. Calculate initial stop
	// 1. 计算初始止损
	initialStop := calc.CalculateInitialStop(symbol, entryPrice, atr, "long")
	expectedInitialStop := entryPrice - cfg.InitialATRMultiplier*atr
	if math.Abs(initialStop-expectedInitialStop) > 0.01 {
		t.Errorf("Initial stop = %.2f, expected %.2f", initialStop, expectedInitialStop)
	}

	// 2. Price rises to 52000, calculate trailing stop
	// 2. 价格上涨到 52000，计算追踪止损
	highestPrice := 52000.0
	trailingStop1 := calc.CalculateTrailingStop(symbol, highestPrice, atr, "long")
	expectedTrailing1 := highestPrice - cfg.TrailingATRMultiplier*atr
	if math.Abs(trailingStop1-expectedTrailing1) > 0.01 {
		t.Errorf("Trailing stop 1 = %.2f, expected %.2f", trailingStop1, expectedTrailing1)
	}

	// 3. Validate update (should move up)
	// 3. 验证更新（应该向上移动）
	if !calc.IsValidUpdate("long", initialStop, trailingStop1) {
		t.Error("Trailing stop should be valid (moving up)")
	}

	// 4. Check if update threshold is met
	// 4. 检查是否超过更新阈值
	if !calc.ShouldUpdate(symbol, initialStop, trailingStop1) {
		t.Error("Change should exceed threshold")
	}

	// 5. Price rises to 53000, calculate new trailing stop
	// 5. 价格上涨到 53000，计算新的追踪止损
	highestPrice = 53000.0
	trailingStop2 := calc.CalculateTrailingStop(symbol, highestPrice, atr, "long")
	expectedTrailing2 := highestPrice - cfg.TrailingATRMultiplier*atr
	if math.Abs(trailingStop2-expectedTrailing2) > 0.01 {
		t.Errorf("Trailing stop 2 = %.2f, expected %.2f", trailingStop2, expectedTrailing2)
	}

	// 6. Validate update (should move up from previous trailing stop)
	// 6. 验证更新（应该从之前的追踪止损向上移动）
	if !calc.IsValidUpdate("long", trailingStop1, trailingStop2) {
		t.Error("New trailing stop should be higher than previous")
	}

	// 7. Try to move stop down (should be invalid)
	// 7. 尝试向下移动止损（应该无效）
	if calc.IsValidUpdate("long", trailingStop2, trailingStop1) {
		t.Error("Moving stop down should be invalid for long position")
	}

	t.Logf("Scenario test passed:")
	t.Logf("  Entry: $%.2f", entryPrice)
	t.Logf("  Initial stop: $%.2f", initialStop)
	t.Logf("  Trailing stop 1 (@ $52000): $%.2f", trailingStop1)
	t.Logf("  Trailing stop 2 (@ $53000): $%.2f", trailingStop2)
}
