package executors

import (
	"context"
	"testing"

	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/logger"
)

// TestBinanceExecutor_SetupExchange 测试交易所设置（需要有效的 API key）
// TestBinanceExecutor_SetupExchange tests exchange setup (requires valid API key)
func TestBinanceExecutor_SetupExchange(t *testing.T) {
	// Skip instead of os.Exit: os.Exit aborts the whole test binary, so a
	// missing local test/.env used to fail every test in this package.
	// 用 Skip 取代 os.Exit：os.Exit 会终止整个测试进程，
	// 因此本地缺少 test/.env 时会导致本包所有测试一并失败。
	cfg, err := config.LoadConfig("../../test/.env")
	if err != nil {
		t.Skipf("跳过：需要本地 test/.env 配置 (%v)", err)
	}

	log := logger.NewColorLogger(true)
	executor := NewBinanceExecutor(cfg, log)
	err = executor.SetupExchange(context.Background(), "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("failed to setup exchange: %v", err)
	}
	t.Logf("setup exchange success")
}

// TestBinanceConnecting 测试币安连接（带代理，使用公开 API 不需要 API key）
// TestBinanceConnecting tests Binance connection (with proxy, uses public API without API key)
func TestBinanceConnecting(t *testing.T) {
	cfg := &config.Config{
		BinanceAPIKey:               "",
		BinanceAPISecret:            "",
		BinanceProxy:                "http://192.168.0.226:6152",
		BinanceProxyInsecureSkipTLS: true, // 设置为 true 以跳过 TLS 验证（某些代理需要）/ Set to true to skip TLS verification (required by some proxies)
		BinanceLeverage:             10,
		BinanceTestMode:             false,
		BinancePositionMode:         "oneway",
	}

	log := logger.NewColorLogger(true)

	// 使用 NewBinanceExecutor 创建执行器（会自动配置代理）
	// Create executor using NewBinanceExecutor (automatically configures proxy)
	executor := NewBinanceExecutor(cfg, log)

	// 测试 Ping（公开 API，不需要 API key）
	// Test Ping (public API, no API key required)
	//
	// The proxy above is a LAN address, so this can only run on a machine that
	// has it. Skip rather than fail when it is unreachable, otherwise the whole
	// package reports red on any other machine.
	// 上面的代理是局域网地址，只有具备该环境的机器才能运行本测试。
	// 不可达时选择跳过而不是失败，否则在其他机器上整个包都会报红。
	err := executor.client.NewPingService().Do(context.Background())
	if err != nil {
		t.Skipf("跳过：无法通过代理 %s 访问币安 (%v)", cfg.BinanceProxy, err)
	}
	t.Logf("✅ Successfully connected to Binance via proxy!")
}
