package dataflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oak/crypto-trading-bot/internal/config"
)

// TestBinanceFetchKlines 测试从币安获取 K 线数据
// 注意：K线数据是公开接口，不需要 API key
// 运行方式：go test -v ./internal/dataflows -run TestBinanceFetchKlines
func TestBinanceFetchKlines(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:    "", // 公开数据不需要 API key
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()
	symbol := "BTCUSDT"
	timeframe := "1h"
	lookbackDays := 1
	t.Logf("获取 %s K 线，时间周期 %s，回看天数 %d", symbol, timeframe, lookbackDays)
	ohlcvData, err := marketData.GetOHLCV(ctx, symbol, timeframe, lookbackDays)
	if err != nil {
		skipIfBinanceUnreachable(t, err)
		t.Fatalf("获取 %s K 线失败: %v", timeframe, err)
	}

	if len(ohlcvData) == 0 {
		t.Fatalf("%s 返回的数据为空", symbol)
	}

	t.Logf("✅ %s: 成功获取 %d 根 K 线（无需 API key）", timeframe, len(ohlcvData))

	// 打印最新价格
	latestPrice := ohlcvData[len(ohlcvData)-1].Close
	t.Logf("最新价格: $%.2f", latestPrice)
}

// TestBinanceFetchMultipleTimeframes 测试获取多个时间周期的数据
func TestBinanceFetchMultipleTimeframes(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:    "", // 公开数据不需要 API key
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()
	symbol := "BTCUSDT"

	// 测试不同的时间周期
	timeframes := []struct {
		tf           string
		lookbackDays int
		minCandles   int // 期望的最少 K 线数量
	}{
		{"15m", 1, 80}, // 1 天约 96 根 15 分钟 K 线
		{"1h", 1, 20},  // 1 天约 24 根 1 小时 K 线
		{"4h", 2, 10},  // 2 天约 12 根 4 小时 K 线
		{"1d", 7, 5},   // 7 天约 7 根 1 日 K 线
	}

	for _, tc := range timeframes {
		t.Run(tc.tf, func(t *testing.T) {
			ohlcvData, err := marketData.GetOHLCV(ctx, symbol, tc.tf, tc.lookbackDays)
			if err != nil {
				skipIfBinanceUnreachable(t, err)
				t.Fatalf("获取 %s K 线失败: %v", tc.tf, err)
			}

			if len(ohlcvData) < tc.minCandles {
				t.Errorf("期望至少 %d 根 K 线，实际获取: %d", tc.minCandles, len(ohlcvData))
			}

			t.Logf("✅ %s: 成功获取 %d 根 K 线（无需 API key）", tc.tf, len(ohlcvData))
		})
	}
}

// TestBinanceFetchWithIndicators 测试获取数据后计算技术指标
func TestBinanceFetchWithIndicators(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:    "", // 公开数据
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()

	// 获取足够的数据来计算 200 日均线
	ohlcvData, err := marketData.GetOHLCV(ctx, "BTCUSDT", "1d", 250)
	if err != nil {
		skipIfBinanceUnreachable(t, err)
		t.Fatalf("获取 K 线数据失败: %v", err)
	}

	t.Logf("获取了 %d 根日线数据", len(ohlcvData))

	// 计算技术指标
	indicators := CalculateIndicators(ohlcvData)

	// 验证指标长度
	if len(indicators.RSI) != len(ohlcvData) {
		t.Errorf("RSI 长度不匹配: 期望 %d, 实际 %d", len(ohlcvData), len(indicators.RSI))
	}

	if len(indicators.MACD) != len(ohlcvData) {
		t.Errorf("MACD 长度不匹配: 期望 %d, 实际 %d", len(ohlcvData), len(indicators.MACD))
	}

	// 获取最新的指标值
	lastIdx := len(ohlcvData) - 1
	latestRSI := indicators.RSI[lastIdx]
	latestMACD := indicators.MACD[lastIdx]
	latestSMA20 := indicators.SMA_20[lastIdx]

	t.Logf("最新指标值：")
	t.Logf("  RSI(14): %.2f", latestRSI)
	t.Logf("  MACD: %.2f", latestMACD)
	t.Logf("  SMA(20): %.2f", latestSMA20)
	t.Logf("  当前价格: %.2f", ohlcvData[lastIdx].Close)

	// 验证 RSI 在合理范围内
	if latestRSI < 0 || latestRSI > 100 {
		t.Errorf("RSI 应该在 0-100 之间，实际: %.2f", latestRSI)
	}
}

// TestBinanceAPIRateLimit 测试 API 速率限制处理
func TestBinanceAPIRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过速率限制测试（运行时间较长）")
	}

	cfg := &config.Config{
		BinanceAPIKey:    "", // 公开数据
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()

	// 连续请求多次，测试是否会触发速率限制
	requestCount := 5
	successCount := 0

	for i := 0; i < requestCount; i++ {
		_, err := marketData.GetOHLCV(ctx, "BTCUSDT", "1h", 1)
		if err != nil {
			skipIfBinanceUnreachable(t, err)
			t.Logf("请求 %d 失败: %v", i+1, err)
		} else {
			successCount++
		}

		// 请求之间稍微延迟，避免过快
		time.Sleep(200 * time.Millisecond)
	}

	t.Logf("完成 %d 次请求，成功 %d 次（无需 API key）", requestCount, successCount)

	if successCount == 0 {
		t.Error("所有请求都失败了，可能是网络问题")
	}
}

// TestBinanceDataQuality 测试数据质量
func TestBinanceDataQuality(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:    "", // 公开数据
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()

	ohlcvData, err := marketData.GetOHLCV(ctx, "BTCUSDT", "1h", 7)
	if err != nil {
		skipIfBinanceUnreachable(t, err)
		t.Fatalf("获取 K 线数据失败: %v", err)
	}

	// 检查数据质量
	issues := 0

	for i, candle := range ohlcvData {
		// 检查是否有异常的价格关系
		if candle.High < candle.Low {
			t.Errorf("K线 %d: 最高价 (%.2f) < 最低价 (%.2f)", i, candle.High, candle.Low)
			issues++
		}

		if candle.High < candle.Open || candle.High < candle.Close {
			t.Errorf("K线 %d: 最高价 (%.2f) 不是真正的最高", i, candle.High)
			issues++
		}

		if candle.Low > candle.Open || candle.Low > candle.Close {
			t.Errorf("K线 %d: 最低价 (%.2f) 不是真正的最低", i, candle.Low)
			issues++
		}

		// 检查价格是否合理（BTC 通常在 10k-200k 范围）
		if candle.Close < 1000 || candle.Close > 200000 {
			t.Logf("警告: K线 %d 的收盘价 (%.2f) 看起来不太正常", i, candle.Close)
		}
	}

	if issues > 0 {
		t.Errorf("发现 %d 个数据质量问题", issues)
	} else {
		t.Log("✅ 数据质量检查通过")
	}
}

// TestBinanceDifferentSymbols 测试不同的交易对
func TestBinanceDifferentSymbols(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:    "",
		BinanceAPISecret: "",
		BinanceTestMode:  false,
	}

	marketData := NewMarketData(cfg)
	ctx := context.Background()

	symbols := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT"}

	for _, symbol := range symbols {
		t.Run(symbol, func(t *testing.T) {
			ohlcvData, err := marketData.GetOHLCV(ctx, symbol, "1h", 1)
			if err != nil {
				skipIfBinanceUnreachable(t, err)
				t.Fatalf("获取 %s K 线失败: %v", symbol, err)
			}

			if len(ohlcvData) == 0 {
				t.Fatalf("%s 返回的数据为空", symbol)
			}

			latestPrice := ohlcvData[len(ohlcvData)-1].Close
			t.Logf("✅ %s 最新价格: $%.2f", symbol, latestPrice)
		})
	}
}

// skipIfBinanceUnreachable skips a live-API test when Binance cannot be reached.
// skipIfBinanceUnreachable 在无法访问币安时跳过实盘接口测试
//
// These tests hit the public REST API directly, so they fail on any machine
// without egress to Binance — notably from a region Binance geo-blocks, where
// the API answers with an "Eligibility" notice rather than data. That is an
// environment condition, not a defect, so it should not be reported as red.
// 这些测试直接请求公开 REST 接口，因此在任何无法访问币安的机器上都会失败——
// 尤其是在币安地域限制的地区，接口会返回 "Eligibility" 提示而非数据。
// 这属于环境限制而非代码缺陷，不应被报告为失败。
func skipIfBinanceUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, marker := range []string{
		"restricted location",
		"Eligibility",
		"no such host",
		"connection refused",
		"unexpected EOF",
		"i/o timeout",
		"TLS handshake",
	} {
		if strings.Contains(msg, marker) {
			t.Skipf("跳过：当前环境无法访问币安公开接口 (%v)", err)
		}
	}
}
