package dataflows

import (
	"context"
	"fmt"

	"math"
	"testing"
	"time"

	"github.com/oak/crypto-trading-bot/internal/config"
)

func TestCalculateSMA(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	period := 3

	result := calculateSMA(data, period)

	// 前两个值应该是 NaN（因为周期是 3）
	if !math.IsNaN(result[0]) || !math.IsNaN(result[1]) {
		t.Errorf("First two values should be NaN")
	}

	// 第三个值应该是 (1+2+3)/3 = 2
	expected := 2.0
	if math.Abs(result[2]-expected) > 0.0001 {
		t.Errorf("SMA[2]: expected %f, got %f", expected, result[2])
	}

	// 最后一个值应该是 (8+9+10)/3 = 9
	expected = 9.0
	if math.Abs(result[9]-expected) > 0.0001 {
		t.Errorf("SMA[9]: expected %f, got %f", expected, result[9])
	}
}

func TestCalculateEMA(t *testing.T) {
	data := []float64{22.27, 22.19, 22.08, 22.17, 22.18, 22.13, 22.23, 22.43, 22.24, 22.29}
	period := 5

	result := calculateEMA(data, period)

	// 检查结果长度
	if len(result) != len(data) {
		t.Errorf("EMA length: expected %d, got %d", len(data), len(result))
	}

	// 前几个值应该是 NaN
	if !math.IsNaN(result[0]) {
		t.Errorf("First value should be NaN")
	}

	// EMA 应该是递增的（在这个数据集中）
	if result[len(result)-1] < 22.0 || result[len(result)-1] > 23.0 {
		t.Errorf("EMA last value seems incorrect: %f", result[len(result)-1])
	}
}

func TestCalculateRSI(t *testing.T) {
	// 使用已知的测试数据
	data := []float64{
		44.34, 44.09, 43.61, 44.33, 44.83,
		45.10, 45.42, 45.84, 46.08, 45.89,
		46.03, 45.61, 46.28, 46.28, 46.00,
		46.03, 46.41, 46.22, 45.64,
	}
	period := 14

	result := calculateRSI(data, period)

	// 检查结果长度
	if len(result) != len(data) {
		t.Errorf("RSI length: expected %d, got %d", len(data), len(result))
	}

	// RSI 应该在 0-100 之间
	for i, val := range result {
		if !math.IsNaN(val) {
			if val < 0 || val > 100 {
				t.Errorf("RSI[%d] out of range [0,100]: %f", i, val)
			}
		}
	}

	// 最后一个 RSI 值应该在合理范围内（约 70 左右，因为是上升趋势）
	lastRSI := result[len(result)-1]
	if lastRSI < 50 || lastRSI > 100 {
		t.Errorf("RSI last value seems incorrect: %f", lastRSI)
	}
}

func TestCalculateMACD(t *testing.T) {
	// 生成测试数据
	data := make([]float64, 100)
	for i := range data {
		data[i] = 100.0 + float64(i)*0.5 // 上升趋势
	}

	macd, signal := calculateMACD(data)

	// 检查结果长度
	if len(macd) != len(data) {
		t.Errorf("MACD length: expected %d, got %d", len(data), len(macd))
	}
	if len(signal) != len(data) {
		t.Errorf("Signal length: expected %d, got %d", len(data), len(signal))
	}

	// 前面的值应该是 NaN
	if !math.IsNaN(macd[0]) {
		t.Errorf("First MACD value should be NaN")
	}

	// 在上升趋势中，MACD 应该是正值
	lastMACD := macd[len(macd)-1]
	if math.IsNaN(lastMACD) || lastMACD <= 0 {
		t.Errorf("MACD in uptrend should be positive, got: %f", lastMACD)
	}
}

func TestCalculateBollingerBands(t *testing.T) {
	data := []float64{
		22.27, 22.19, 22.08, 22.17, 22.18,
		22.13, 22.23, 22.43, 22.24, 22.29,
		22.15, 22.39, 22.38, 22.61, 23.36,
		24.05, 23.75, 23.83, 23.95, 23.63,
	}
	period := 20
	stdDev := 2.0

	upper, middle, lower := calculateBollingerBands(data, period, stdDev)

	// 检查长度
	if len(upper) != len(data) || len(middle) != len(data) || len(lower) != len(data) {
		t.Errorf("Bollinger Bands length mismatch")
	}

	// 检查最后一个值的关系：upper > middle > lower
	lastIdx := len(data) - 1
	if !math.IsNaN(upper[lastIdx]) {
		if !(upper[lastIdx] > middle[lastIdx] && middle[lastIdx] > lower[lastIdx]) {
			t.Errorf("Bollinger Bands relationship incorrect: upper=%f, middle=%f, lower=%f",
				upper[lastIdx], middle[lastIdx], lower[lastIdx])
		}
	}
}

func TestCalculateATR(t *testing.T) {
	highs := []float64{48.70, 48.72, 48.90, 48.87, 48.82, 49.05, 49.20, 49.35, 49.92, 50.19}
	lows := []float64{47.79, 48.14, 48.39, 48.37, 48.24, 48.64, 48.94, 48.86, 49.50, 49.87}
	closes := []float64{48.16, 48.61, 48.75, 48.63, 48.74, 49.03, 49.07, 49.32, 49.91, 50.13}
	period := 14

	result := calculateATR(highs, lows, closes, period)

	// 检查长度
	if len(result) != len(highs) {
		t.Errorf("ATR length: expected %d, got %d", len(highs), len(result))
	}

	// ATR 应该是正值
	for i, val := range result {
		if !math.IsNaN(val) {
			if val <= 0 {
				t.Errorf("ATR[%d] should be positive: %f", i, val)
			}
		}
	}
}

func TestTechnicalIndicatorsStructure(t *testing.T) {
	// 创建测试用的 OHLCV 数据
	ohlcvData := make([]OHLCV, 50)
	baseTime := time.Now()
	for i := range ohlcvData {
		price := 100.0 + float64(i)*0.5
		ohlcvData[i] = OHLCV{
			Timestamp: baseTime.Add(time.Duration(i) * time.Hour),
			Open:      price - 0.2,
			High:      price + 0.3,
			Low:       price - 0.5,
			Close:     price,
			Volume:    1000.0 + float64(i)*10,
		}
	}

	// 手动计算一些指标来验证
	closes := make([]float64, len(ohlcvData))
	highs := make([]float64, len(ohlcvData))
	lows := make([]float64, len(ohlcvData))
	for i, k := range ohlcvData {
		closes[i] = k.Close
		highs[i] = k.High
		lows[i] = k.Low
	}

	rsi := calculateRSI(closes, 14)
	macd, signal := calculateMACD(closes)
	upper, middle, lower := calculateBollingerBands(closes, 20, 2.0)
	atr := calculateATR(highs, lows, closes, 14)

	// 检查所有指标的长度
	if len(rsi) != len(ohlcvData) {
		t.Errorf("RSI length mismatch: expected %d, got %d", len(ohlcvData), len(rsi))
	}
	if len(macd) != len(ohlcvData) {
		t.Errorf("MACD length mismatch: expected %d, got %d", len(ohlcvData), len(macd))
	}
	if len(signal) != len(ohlcvData) {
		t.Errorf("Signal length mismatch: expected %d, got %d", len(ohlcvData), len(signal))
	}
	if len(upper) != len(ohlcvData) {
		t.Errorf("BollingerUpper length mismatch: expected %d, got %d", len(ohlcvData), len(upper))
	}
	if len(middle) != len(ohlcvData) {
		t.Errorf("BollingerMiddle length mismatch: expected %d, got %d", len(ohlcvData), len(middle))
	}
	if len(lower) != len(ohlcvData) {
		t.Errorf("BollingerLower length mismatch: expected %d, got %d", len(ohlcvData), len(lower))
	}
	if len(atr) != len(ohlcvData) {
		t.Errorf("ATR length mismatch: expected %d, got %d", len(ohlcvData), len(atr))
	}

	// 检查最新值是否有效（非 NaN）
	lastIdx := len(ohlcvData) - 1
	if math.IsNaN(rsi[lastIdx]) {
		t.Errorf("Latest RSI should not be NaN")
	}
}

// TestCalculateIndicators tests the CalculateIndicators function
// TestCalculateIndicators 测试计算指标函数
func TestCalculateIndicators(t *testing.T) {
	t.Run("EmptyData", func(t *testing.T) {
		// 测试空数据
		emptyData := []OHLCV{}
		indicators := CalculateIndicators(emptyData)

		// 应该返回空的技术指标结构
		if indicators == nil {
			t.Fatal("CalculateIndicators should not return nil for empty data")
		}

		if len(indicators.RSI) != 0 {
			t.Errorf("Expected empty RSI array, got length %d", len(indicators.RSI))
		}
	})

	t.Run("NormalData", func(t *testing.T) {
		// 测试正常数据
		// 创建足够的测试数据（至少需要 200+ 个数据点以计算 SMA200）
		dataPoints := 250
		ohlcvData := make([]OHLCV, dataPoints)
		baseTime := time.Now()

		for i := range ohlcvData {
			price := 100.0 + float64(i)*0.5 + math.Sin(float64(i)*0.1)*5 // 添加一些波动
			ohlcvData[i] = OHLCV{
				Timestamp: baseTime.Add(time.Duration(i) * time.Hour),
				Open:      price - 0.5,
				High:      price + 1.0,
				Low:       price - 1.0,
				Close:     price,
				Volume:    1000.0 + float64(i)*10,
			}
		}

		indicators := CalculateIndicators(ohlcvData)

		// 验证所有指标数组长度与输入数据长度一致
		// Verify all indicator arrays have same length as input data
		if len(indicators.RSI) != dataPoints {
			t.Errorf("RSI length: expected %d, got %d", dataPoints, len(indicators.RSI))
		}
		if len(indicators.RSI_7) != dataPoints {
			t.Errorf("RSI_7 length: expected %d, got %d", dataPoints, len(indicators.RSI_7))
		}
		if len(indicators.MACD) != dataPoints {
			t.Errorf("MACD length: expected %d, got %d", dataPoints, len(indicators.MACD))
		}
		if len(indicators.Signal) != dataPoints {
			t.Errorf("Signal length: expected %d, got %d", dataPoints, len(indicators.Signal))
		}
		if len(indicators.BB_Upper) != dataPoints {
			t.Errorf("BB_Upper length: expected %d, got %d", dataPoints, len(indicators.BB_Upper))
		}
		if len(indicators.BB_Middle) != dataPoints {
			t.Errorf("BB_Middle length: expected %d, got %d", dataPoints, len(indicators.BB_Middle))
		}
		if len(indicators.BB_Lower) != dataPoints {
			t.Errorf("BB_Lower length: expected %d, got %d", dataPoints, len(indicators.BB_Lower))
		}
		if len(indicators.SMA_20) != dataPoints {
			t.Errorf("SMA_20 length: expected %d, got %d", dataPoints, len(indicators.SMA_20))
		}
		if len(indicators.SMA_50) != dataPoints {
			t.Errorf("SMA_50 length: expected %d, got %d", dataPoints, len(indicators.SMA_50))
		}
		if len(indicators.SMA_200) != dataPoints {
			t.Errorf("SMA_200 length: expected %d, got %d", dataPoints, len(indicators.SMA_200))
		}
		if len(indicators.EMA_12) != dataPoints {
			t.Errorf("EMA_12 length: expected %d, got %d", dataPoints, len(indicators.EMA_12))
		}
		if len(indicators.EMA_20) != dataPoints {
			t.Errorf("EMA_20 length: expected %d, got %d", dataPoints, len(indicators.EMA_20))
		}
		if len(indicators.EMA_26) != dataPoints {
			t.Errorf("EMA_26 length: expected %d, got %d", dataPoints, len(indicators.EMA_26))
		}
		if len(indicators.ATR_7) != dataPoints {
			t.Errorf("ATR length: expected %d, got %d", dataPoints, len(indicators.ATR_7))
		}
		if len(indicators.ATR_3) != dataPoints {
			t.Errorf("ATR_3 length: expected %d, got %d", dataPoints, len(indicators.ATR_3))
		}
		if len(indicators.Volume) != dataPoints {
			t.Errorf("Volume length: expected %d, got %d", dataPoints, len(indicators.Volume))
		}
		if len(indicators.ADX) != dataPoints {
			t.Errorf("ADX length: expected %d, got %d", dataPoints, len(indicators.ADX))
		}
		if len(indicators.DI_Plus) != dataPoints {
			t.Errorf("DI_Plus length: expected %d, got %d", dataPoints, len(indicators.DI_Plus))
		}
		if len(indicators.DI_Minus) != dataPoints {
			t.Errorf("DI_Minus length: expected %d, got %d", dataPoints, len(indicators.DI_Minus))
		}
		if len(indicators.VolumeRatio) != dataPoints {
			t.Errorf("VolumeRatio length: expected %d, got %d", dataPoints, len(indicators.VolumeRatio))
		}

		// 验证最新值不是 NaN（对于有足够数据的指标）
		// Verify latest values are not NaN (for indicators with sufficient data)
		lastIdx := dataPoints - 1

		if math.IsNaN(indicators.RSI[lastIdx]) {
			t.Error("Latest RSI value should not be NaN")
		}
		if math.IsNaN(indicators.RSI_7[lastIdx]) {
			t.Error("Latest RSI_7 value should not be NaN")
		}
		if math.IsNaN(indicators.SMA_20[lastIdx]) {
			t.Error("Latest SMA_20 value should not be NaN")
		}
		if math.IsNaN(indicators.SMA_50[lastIdx]) {
			t.Error("Latest SMA_50 value should not be NaN")
		}
		if math.IsNaN(indicators.SMA_200[lastIdx]) {
			t.Error("Latest SMA_200 value should not be NaN")
		}

		// 验证 RSI 值在合理范围内（0-100）
		// Verify RSI values are in valid range (0-100)
		if !math.IsNaN(indicators.RSI[lastIdx]) {
			if indicators.RSI[lastIdx] < 0 || indicators.RSI[lastIdx] > 100 {
				t.Errorf("RSI out of range [0,100]: %f", indicators.RSI[lastIdx])
			}
		}
		if !math.IsNaN(indicators.RSI_7[lastIdx]) {
			if indicators.RSI_7[lastIdx] < 0 || indicators.RSI_7[lastIdx] > 100 {
				t.Errorf("RSI_7 out of range [0,100]: %f", indicators.RSI_7[lastIdx])
			}
		}

		// 验证布林带关系：upper > middle > lower
		// Verify Bollinger Bands relationship: upper > middle > lower
		if !math.IsNaN(indicators.BB_Upper[lastIdx]) &&
			!math.IsNaN(indicators.BB_Middle[lastIdx]) &&
			!math.IsNaN(indicators.BB_Lower[lastIdx]) {
			if indicators.BB_Upper[lastIdx] <= indicators.BB_Middle[lastIdx] {
				t.Error("BB_Upper should be greater than BB_Middle")
			}
			if indicators.BB_Middle[lastIdx] <= indicators.BB_Lower[lastIdx] {
				t.Error("BB_Middle should be greater than BB_Lower")
			}
		}

		// 验证 ATR 为正值
		// Verify ATR is positive
		if !math.IsNaN(indicators.ATR_7[lastIdx]) && indicators.ATR_7[lastIdx] <= 0 {
			t.Errorf("ATR should be positive, got: %f", indicators.ATR_7[lastIdx])
		}
		if !math.IsNaN(indicators.ATR_3[lastIdx]) && indicators.ATR_3[lastIdx] <= 0 {
			t.Errorf("ATR_3 should be positive, got: %f", indicators.ATR_3[lastIdx])
		}

		// 验证成交量数据正确复制
		// Verify volume data is correctly copied
		for i := range ohlcvData {
			if indicators.Volume[i] != ohlcvData[i].Volume {
				t.Errorf("Volume[%d]: expected %f, got %f", i, ohlcvData[i].Volume, indicators.Volume[i])
			}
		}
	})

	t.Run("SmallDataSet", func(t *testing.T) {
		// 测试小数据集（不足以计算长期指标如 SMA_200）
		// Test small dataset (insufficient for long-term indicators like SMA_200)
		// 使用 50 个数据点，足够计算 ADX(14) 但不足以计算 SMA_200
		// Use 50 data points, enough for ADX(14) but not for SMA_200
		smallData := make([]OHLCV, 50)
		baseTime := time.Now()

		for i := range smallData {
			price := 100.0 + float64(i)
			smallData[i] = OHLCV{
				Timestamp: baseTime.Add(time.Duration(i) * time.Hour),
				Open:      price - 0.5,
				High:      price + 0.5,
				Low:       price - 0.5,
				Close:     price,
				Volume:    1000.0,
			}
		}

		indicators := CalculateIndicators(smallData)

		// 应该不会崩溃，但某些指标值会是 NaN
		// Should not crash, but some indicator values will be NaN
		if indicators == nil {
			t.Fatal("CalculateIndicators should not return nil")
		}

		if len(indicators.RSI) != len(smallData) {
			t.Errorf("Indicator arrays should match input length")
		}

		// SMA_200 对于小数据集应该全是 NaN
		// SMA_200 should be all NaN for dataset smaller than 200
		allNaN := true
		for _, val := range indicators.SMA_200 {
			if !math.IsNaN(val) {
				allNaN = false
				break
			}
		}
		if !allNaN {
			t.Error("SMA_200 should be all NaN for dataset smaller than 200")
		}
	})
}

// TestConvertTimeframe tests the convertTimeframe helper function
// TestConvertTimeframe 测试时间周期转换辅助函数
func TestConvertTimeframe(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
		desc     string
	}{
		// 分钟级 - Minute intervals
		{"1m", "1m", "1 minute"},
		{"3m", "3m", "3 minutes"},
		{"5m", "5m", "5 minutes"},
		{"15m", "15m", "15 minutes"},
		{"30m", "30m", "30 minutes"},

		// 小时级 - Hour intervals
		{"1h", "1h", "1 hour"},
		{"2h", "2h", "2 hours"},
		{"4h", "4h", "4 hours"},
		{"6h", "6h", "6 hours"},
		{"8h", "8h", "8 hours"},
		{"12h", "12h", "12 hours"},

		// 天/周/月级 - Day/Week/Month intervals
		{"1d", "1d", "1 day"},
		{"3d", "3d", "3 days"},
		{"1w", "1w", "1 week"},
		{"1M", "1M", "1 month"},

		// 无效输入 - Invalid inputs
		{"invalid", "1h", "default fallback for invalid"},
		{"", "1h", "default fallback for empty string"},
		{"2m", "1h", "unsupported 2m interval"},
		{"10m", "1h", "unsupported 10m interval"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			result := convertTimeframe(tc.input)
			if result != tc.expected {
				t.Errorf("convertTimeframe(%q): expected %q, got %q", tc.input, tc.expected, result)
			}
		})
	}
}

// TestGetOHLCV_UnitTests tests GetOHLCV function's basic properties
// TestGetOHLCV_UnitTests 测试 GetOHLCV 函数的基本属性
// Note: For full integration tests with actual Binance API calls,
// see binance_integration_test.go
// 注意：完整的集成测试（包括实际 Binance API 调用）请参见 binance_integration_test.go
func TestGetOHLCV_UnitTests(t *testing.T) {
	t.Run("GetOHLCV", func(t *testing.T) {
		cfg := &config.Config{
			BinanceAPIKey:    "",
			BinanceAPISecret: "",
			BinanceTestMode:  true,
			BinanceProxy:     "http://127.0.0.1:6152",
		}

		marketData := NewMarketData(cfg)
		ctx := context.Background()
		//ctx, cancel := context.WithCancel(context.Background())
		//cancel() // Cancel immediately
		timeframe := "3m"

		ohlcvData, err := marketData.GetOHLCV(ctx, "SOLUSDT", timeframe, 2)
		if err == nil {
			t.Error("GetOHLCV should return error for cancelled context")
		}
		// Calculate indicators for primary timeframe
		// 计算主时间周期的指标
		indicators := CalculateIndicators(ohlcvData)

		// Generate primary timeframe report
		// 生成主时间周期报告
		report := FormatIndicatorReport("SOLUSDT", timeframe, ohlcvData, indicators)
		fmt.Print(report)

	})

}

// TestOHLCVStructure tests the OHLCV data structure
// TestOHLCVStructure 测试 OHLCV 数据结构
func TestOHLCVStructure(t *testing.T) {
	t.Run("BasicFields", func(t *testing.T) {
		// 测试 OHLCV 结构体字段
		// Test OHLCV struct fields
		now := time.Now()
		candle := OHLCV{
			Timestamp: now,
			Open:      100.0,
			High:      105.0,
			Low:       95.0,
			Close:     102.0,
			Volume:    1000.0,
		}

		if candle.Timestamp != now {
			t.Error("Timestamp field not set correctly")
		}
		if candle.Open != 100.0 {
			t.Error("Open field not set correctly")
		}
		if candle.High != 105.0 {
			t.Error("High field not set correctly")
		}
		if candle.Low != 95.0 {
			t.Error("Low field not set correctly")
		}
		if candle.Close != 102.0 {
			t.Error("Close field not set correctly")
		}
		if candle.Volume != 1000.0 {
			t.Error("Volume field not set correctly")
		}
	})

	t.Run("ValidPriceRelationship", func(t *testing.T) {
		// 测试价格关系验证
		// Test price relationship validation
		validCandle := OHLCV{
			Timestamp: time.Now(),
			Open:      100.0,
			High:      105.0,
			Low:       95.0,
			Close:     102.0,
			Volume:    1000.0,
		}

		// High 应该是最高价
		// High should be the highest price
		if validCandle.High < validCandle.Open || validCandle.High < validCandle.Close {
			t.Error("High should be >= Open and Close")
		}

		// Low 应该是最低价
		// Low should be the lowest price
		if validCandle.Low > validCandle.Open || validCandle.Low > validCandle.Close {
			t.Error("Low should be <= Open and Close")
		}

		// High >= Low
		if validCandle.High < validCandle.Low {
			t.Error("High should be >= Low")
		}
	})
}
