package config

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// 从项目根目录的 test 目录加载测试配置
	// Load test config from test directory in project root
	// test/.env is a local, git-ignored fixture; skip when it is absent so the
	// suite is runnable on a fresh clone and in CI.
	// test/.env 是本地的、被 git 忽略的测试配置；缺失时跳过，
	// 以便在全新克隆和 CI 环境中也能跑通测试。
	cfg, err := LoadConfig("../../test/.env")
	if err != nil {
		t.Skipf("跳过：需要本地 test/.env 配置 (%v)", err)
	}

	// 验证配置是否正确加载
	// Verify config is loaded correctly
	if len(cfg.CryptoSymbols) == 0 {
		t.Errorf("Expected CryptoSymbols to be set, got empty list")
	}

	t.Logf("Successfully loaded config with CryptoSymbols: %v", cfg.CryptoSymbols)
}

func TestCalculateLookbackDays(t *testing.T) {
	tests := []struct {
		timeframe string
		expected  int
	}{
		{"15m", 5},
		{"1h", 10},
		{"4h", 15},
		{"1d", 60},
		{"5m", 10},      // 默认值
		{"unknown", 10}, // 默认值
	}

	for _, tt := range tests {
		t.Run(tt.timeframe, func(t *testing.T) {
			result := calculateLookbackDays(tt.timeframe)
			if result != tt.expected {
				t.Errorf("calculateLookbackDays(%s): expected %d, got %d",
					tt.timeframe, tt.expected, result)
			}
		})
	}
}
