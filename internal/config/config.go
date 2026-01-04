package config

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/oak/crypto-trading-bot/internal/constant"
	"github.com/spf13/viper"
	"os"
	"strings"
)

// Config holds all configuration for the crypto trading bot
type Config struct {
	// Project paths
	ProjectDir   string
	ResultsDir   string
	DataCacheDir string
	DatabasePath string

	// LLM Configuration
	LLMProvider      string
	DeepThinkLLM     string
	QuickThinkLLM    string
	BackendURL       string
	APIKey           string
	TraderPromptPath string // 交易策略 Prompt 文件路径 / Path to trader strategy prompt file

	// Agent behavior
	MaxDebateRounds      int
	MaxRiskDiscussRounds int
	MaxRecurLimit        int

	// Data vendors
	DataVendorStock      string
	DataVendorIndicators string
	DataVendorNews       string
	DataVendorCrypto     string

	// Binance trading configuration
	// 币安交易配置
	BinanceAPIKey               string
	BinanceAPISecret            string
	BinanceProxy                string
	BinanceProxyInsecureSkipTLS bool // 是否跳过代理 TLS 验证（某些代理需要）/ Skip TLS verification for proxy (required by some proxies)
	BinanceLeverage             int  // 固定杠杆（向后兼容）/ Fixed leverage (backward compatible)
	BinanceLeverageMin          int  // 最小杠杆 / Minimum leverage
	BinanceLeverageMax          int  // 最大杠杆 / Maximum leverage
	BinanceLeverageDynamic      bool // 是否启用动态杠杆 / Enable dynamic leverage
	BinanceTestMode             bool
	BinancePositionMode         string

	// Trading parameters
	// 交易参数
	CryptoSymbols      []string // 交易对列表（支持单个或多个，用逗号分隔）/ Trading pairs list (supports single or multiple, comma-separated)
	CryptoTimeframe    string   // K线数据时间间隔 / K-line data timeframe
	TradingInterval    string   // 系统运行间隔（独立于K线间隔）/ System execution interval (independent from K-line timeframe)
	CryptoLookbackDays int
	MaxOpenPosition    int // 单日最大开仓次数（0=不限制）/ Daily max number of opening positions per day (0 = unlimited)
	// PositionSize removed - now uses LLM's position size recommendation
	// 移除 PositionSize - 现在使用 LLM 的仓位建议

	// Multi-timeframe analysis
	// 多时间周期分析
	EnableMultiTimeframe     bool   // 是否启用多时间周期分析 / Enable multi-timeframe analysis
	CryptoLongerTimeframe    string // 更长期的时间周期（如 4h）/ Longer timeframe (e.g., 4h)
	CryptoLongerLookbackDays int    // 更长期时间周期的回看天数 / Lookback days for longer timeframe

	// Analysis options
	// 分析选项
	EnableSentimentAnalysis bool // 是否启用市场情绪分析 / Enable sentiment analysis (CryptoOracle API)

	// Stop-loss management configuration
	// 止损管理配置
	// Note: Trailing stop parameters (update threshold, ATR multiplier, etc.) are configured
	// in internal/executors/trailing_stop_calculator.go for each symbol
	// 注意：追踪止损参数（更新阈值、ATR倍数等）在 internal/executors/trailing_stop_calculator.go 中为每个币种配置
	EnableStopLoss        bool // 是否启用止损管理 / Enable stop-loss management
	TrailingStopATRPeriod int  // 追踪止损的 ATR 周期（从长期时间周期计算，推荐 3/7/14）/ ATR period for trailing stop (calculated from longer timeframe, recommended 3/7/14)

	// Memory system
	UseMemory  bool
	MemoryTopK int

	// Debug options
	DebugMode        bool
	SelectedAnalysts []string
	AutoExecute      bool

	// Web monitoring
	// Web 监控配置
	WebPort     int
	WebUsername string // Web 登录用户名 / Web login username
	WebPassword string // Web 登录密码 / Web login password
}

// LoadConfig loads configuration from .env file or a custom path
// LoadConfig 从 .env 文件或自定义路径加载配置
func LoadConfig(pathToEnv string) (*Config, error) {
	viper.SetConfigType("env")
	viper.AutomaticEnv()

	// Determine which config file to load
	configPath := ".env" // default path / 默认路径
	if pathToEnv != constant.BlankStr {
		configPath = pathToEnv
	}

	viper.SetConfigFile(configPath)

	// Attempt to read config file, but don't fail if it doesn't exist
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file from %s: %w", configPath, err)
		}
	}

	// Set defaults
	setDefaults()

	cfg := &Config{
		// Project paths
		ProjectDir:   getProjectDir(),
		ResultsDir:   viper.GetString("RESULTS_DIR"),
		DataCacheDir: viper.GetString("DATA_CACHE_DIR"),
		DatabasePath: viper.GetString("DATABASE_PATH"),

		// LLM Configuration
		LLMProvider:      viper.GetString("LLM_PROVIDER"),
		DeepThinkLLM:     viper.GetString("DEEP_THINK_LLM"),
		QuickThinkLLM:    viper.GetString("QUICK_THINK_LLM"),
		BackendURL:       viper.GetString("LLM_BACKEND_URL"),
		APIKey:           viper.GetString("OPENAI_API_KEY"),
		TraderPromptPath: viper.GetString("TRADER_PROMPT_PATH"),

		// Agent behavior
		MaxDebateRounds:      viper.GetInt("MAX_DEBATE_ROUNDS"),
		MaxRiskDiscussRounds: viper.GetInt("MAX_RISK_DISCUSS_ROUNDS"),
		MaxRecurLimit:        viper.GetInt("MAX_RECUR_LIMIT"),

		// Data vendors
		DataVendorStock:      viper.GetString("DATA_VENDOR_STOCK"),
		DataVendorIndicators: viper.GetString("DATA_VENDOR_INDICATORS"),
		DataVendorNews:       viper.GetString("DATA_VENDOR_NEWS"),
		DataVendorCrypto:     viper.GetString("DATA_VENDOR_CRYPTO"),

		// Binance trading configuration
		BinanceAPIKey:               viper.GetString("BINANCE_API_KEY"),
		BinanceAPISecret:            viper.GetString("BINANCE_API_SECRET"),
		BinanceProxy:                viper.GetString("BINANCE_PROXY"),
		BinanceProxyInsecureSkipTLS: viper.GetBool("BINANCE_PROXY_INSECURE_SKIP_TLS"),
		BinanceLeverage:             viper.GetInt("BINANCE_LEVERAGE"),
		BinanceTestMode:             viper.GetBool("BINANCE_TEST_MODE"),
		BinancePositionMode:         viper.GetString("BINANCE_POSITION_MODE"),

		// Trading parameters
		CryptoTimeframe:    viper.GetString("CRYPTO_TIMEFRAME"),
		TradingInterval:    viper.GetString("TRADING_INTERVAL"),
		CryptoLookbackDays: viper.GetInt("CRYPTO_LOOKBACK_DAYS"),
		MaxOpenPosition:    viper.GetInt("MAX_OPEN_POSITION"),
		// PositionSize removed - now uses LLM's position size recommendation

		// Multi-timeframe analysis
		// 多时间周期分析
		EnableMultiTimeframe:     viper.GetBool("ENABLE_MULTI_TIMEFRAME"),
		CryptoLongerTimeframe:    viper.GetString("CRYPTO_LONGER_TIMEFRAME"),
		CryptoLongerLookbackDays: viper.GetInt("CRYPTO_LONGER_LOOKBACK_DAYS"),

		// Analysis options
		EnableSentimentAnalysis: viper.GetBool("ENABLE_SENTIMENT_ANALYSIS"),

		// Stop-loss management
		// Trailing stop parameters are configured in internal/executors/trailing_stop_calculator.go
		// 追踪止损参数在 internal/executors/trailing_stop_calculator.go 中配置
		EnableStopLoss:        viper.GetBool("ENABLE_STOPLOSS"),
		TrailingStopATRPeriod: viper.GetInt("TRAILING_STOP_ATR_PERIOD"),

		// Memory system
		UseMemory:  viper.GetBool("USE_MEMORY"),
		MemoryTopK: viper.GetInt("MEMORY_TOP_K"),

		// Debug options
		DebugMode:        viper.GetBool("DEBUG_MODE"),
		SelectedAnalysts: strings.Split(viper.GetString("SELECTED_ANALYSTS"), ","),
		AutoExecute:      viper.GetBool("AUTO_EXECUTE"),

		// Web monitoring
		// Web 监控配置
		WebPort:     viper.GetInt("WEB_PORT"),
		WebUsername: viper.GetString("WEB_USERNAME"),
		WebPassword: viper.GetString("WEB_PASSWORD"),
	}

	// Auto-calculate lookback days if not set
	// 如果未设置回看天数，自动计算
	if cfg.CryptoLookbackDays == 0 {
		cfg.CryptoLookbackDays = calculateLookbackDays(cfg.CryptoTimeframe)
	}

	// Setup multi-timeframe analysis defaults
	// 设置多时间周期分析默认值
	if cfg.EnableMultiTimeframe {
		// Default longer timeframe to 4h if not set
		// 如果未设置更长期时间周期，默认为 4h
		if cfg.CryptoLongerTimeframe == "" {
			cfg.CryptoLongerTimeframe = "4h"
		}

		// Auto-calculate longer timeframe lookback days if not set
		// 如果未设置更长期时间周期的回看天数，自动计算
		if cfg.CryptoLongerLookbackDays == 0 {
			cfg.CryptoLongerLookbackDays = calculateLookbackDays(cfg.CryptoLongerTimeframe)
		}
	}

	// Parse crypto symbols (supports single or multiple, comma-separated)
	// 解析加密货币交易对（支持单个或多个，用逗号分隔）
	symbolsStr := viper.GetString("CRYPTO_SYMBOLS")
	if symbolsStr != "" {
		cfg.CryptoSymbols = strings.Split(symbolsStr, ",")
		// Trim spaces from each symbol
		// 去除每个交易对的空格
		for i := range cfg.CryptoSymbols {
			cfg.CryptoSymbols[i] = strings.TrimSpace(cfg.CryptoSymbols[i])
		}
	} else {
		// Default to BTC/USDT if not specified
		// 如果未指定，默认使用 BTC/USDT
		cfg.CryptoSymbols = []string{"BTC/USDT"}
	}

	// Parse leverage range (support "10-20" format)
	// 解析杠杆范围（支持 "10-20" 格式）
	leverageStr := viper.GetString("BINANCE_LEVERAGE")
	if strings.Contains(leverageStr, "-") {
		// Dynamic leverage: parse min and max
		// 动态杠杆：解析最小值和最大值
		parts := strings.Split(leverageStr, "-")
		if len(parts) == 2 {
			minLev := 0
			maxLev := 0
			fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &minLev)
			fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &maxLev)

			if minLev > 0 && maxLev > 0 && minLev <= maxLev && maxLev <= 125 {
				cfg.BinanceLeverageMin = minLev
				cfg.BinanceLeverageMax = maxLev
				cfg.BinanceLeverageDynamic = true
				cfg.BinanceLeverage = minLev // Default to min for safety
			} else {
				// Invalid range, fallback to default
				// 无效范围，回退到默认值
				cfg.BinanceLeverage = 10
				cfg.BinanceLeverageMin = 10
				cfg.BinanceLeverageMax = 10
				cfg.BinanceLeverageDynamic = false
			}
		}
	} else {
		// Fixed leverage
		// 固定杠杆
		cfg.BinanceLeverageMin = cfg.BinanceLeverage
		cfg.BinanceLeverageMax = cfg.BinanceLeverage
		cfg.BinanceLeverageDynamic = false
	}

	// Setup TradingInterval default (use CRYPTO_TIMEFRAME if not set)
	// 设置 TradingInterval 默认值（如果未设置，使用 CRYPTO_TIMEFRAME）
	if cfg.TradingInterval == "" {
		cfg.TradingInterval = cfg.CryptoTimeframe
	}

	return cfg, nil
}

func setDefaults() {
	viper.SetDefault("RESULTS_DIR", "./crypto_results")
	viper.SetDefault("DATA_CACHE_DIR", "./internal/dataflows/data_cache")
	viper.SetDefault("DATABASE_PATH", "./data/trading.db")

	viper.SetDefault("LLM_PROVIDER", "openai")
	viper.SetDefault("DEEP_THINK_LLM", "gpt-4o")
	viper.SetDefault("QUICK_THINK_LLM", "gpt-4o-mini")
	viper.SetDefault("LLM_BACKEND_URL", "https://api.openai.com/v1")
	viper.SetDefault("TRADER_PROMPT_PATH", "prompts/trader_system.txt")

	viper.SetDefault("MAX_DEBATE_ROUNDS", 2)
	viper.SetDefault("MAX_RISK_DISCUSS_ROUNDS", 2)
	viper.SetDefault("MAX_RECUR_LIMIT", 100)

	viper.SetDefault("DATA_VENDOR_STOCK", "ccxt")
	viper.SetDefault("DATA_VENDOR_INDICATORS", "ccxt")
	viper.SetDefault("DATA_VENDOR_NEWS", "alpha_vantage")
	viper.SetDefault("DATA_VENDOR_CRYPTO", "ccxt")

	viper.SetDefault("BINANCE_LEVERAGE", 10)
	viper.SetDefault("BINANCE_TEST_MODE", true)
	viper.SetDefault("BINANCE_POSITION_MODE", "auto")

	viper.SetDefault("CRYPTO_SYMBOL", "BTC/USDT")
	viper.SetDefault("CRYPTO_TIMEFRAME", "1h")
	viper.SetDefault("MAX_OPEN_POSITION", 0) // 单日最大开仓次数（0=不限制）/ Daily max open positions per day (0 = unlimited)
	// POSITION_SIZE removed - now uses LLM's position size recommendation
	// 移除 POSITION_SIZE - 现在使用 LLM 的仓位建议

	// Analysis defaults
	// 分析选项默认值
	viper.SetDefault("ENABLE_SENTIMENT_ANALYSIS", true) // 默认启用情绪分析 / Enable sentiment analysis by default

	// Stop-loss management defaults
	// 止损管理默认值
	// Trailing stop parameters are configured in internal/executors/trailing_stop_calculator.go
	// 追踪止损参数在 internal/executors/trailing_stop_calculator.go 中配置
	viper.SetDefault("ENABLE_STOPLOSS", true)       // 启用止损管理 / Enable stop-loss management
	viper.SetDefault("TRAILING_STOP_ATR_PERIOD", 7) // 追踪止损 ATR 周期，推荐 3（短期）/7（平衡）/14（长期）/ Trailing stop ATR period, recommended 3 (short) / 7 (balanced) / 14 (long)

	viper.SetDefault("USE_MEMORY", true)
	viper.SetDefault("MEMORY_TOP_K", 3)

	viper.SetDefault("DEBUG_MODE", false)
	viper.SetDefault("SELECTED_ANALYSTS", "market,crypto,sentiment")
	viper.SetDefault("AUTO_EXECUTE", false)

	viper.SetDefault("WEB_PORT", 8080)
	viper.SetDefault("WEB_USERNAME", "admin")
	viper.SetDefault("WEB_PASSWORD", "changeme")
}

func getProjectDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// calculateLookbackDays returns optimal lookback days based on timeframe
func calculateLookbackDays(timeframe string) int {
	switch timeframe {
	case "3m":
		return 3 // ~1440 candles (3 days * 480 candles/day)
	case "15m":
		return 5 // ~480 candles
	case "1h":
		return 10 // ~240 candles
	case "4h":
		return 15 // ~90 candles
	case "1d":
		return 60 // ~60 candles
	default:
		return 10
	}
}

// GetBinanceSymbolFor converts a specific symbol format from "BTC/USDT" to "BTCUSDT"
// GetBinanceSymbolFor 将特定交易对格式从 "BTC/USDT" 转换为 "BTCUSDT"
func (c *Config) GetBinanceSymbolFor(symbol string) string {
	return strings.ReplaceAll(symbol, "/", "")
}

// GetAllBinanceSymbols returns all trading pairs in Binance format
// GetAllBinanceSymbols 返回所有交易对的币安格式
func (c *Config) GetAllBinanceSymbols() []string {
	symbols := make([]string, len(c.CryptoSymbols))
	for i, symbol := range c.CryptoSymbols {
		symbols[i] = strings.ReplaceAll(symbol, "/", "")
	}
	return symbols
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}

	if c.BinanceAPIKey == "" || c.BinanceAPISecret == "" {
		return fmt.Errorf("BINANCE_API_KEY and BINANCE_API_SECRET are required")
	}

	// PositionSize validation removed - now relies on LLM's position size recommendation
	// 移除 PositionSize 验证 - 现在依赖 LLM 的仓位建议

	return nil
}

// SaveToEnv updates specific key-value pairs in the .env file
// SaveToEnv 更新 .env 文件中的特定键值对
func SaveToEnv(envPath string, updates map[string]string) error {
	// Default to .env if not specified
	// 如果未指定，默认为 .env
	if envPath == "" {
		envPath = ".env"
	}

	// Read the existing .env file
	// 读取现有的 .env 文件
	file, err := os.Open(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			// If file doesn't exist, create a new one
			// 如果文件不存在，创建一个新文件
			return createNewEnvFile(envPath, updates)
		}
		return fmt.Errorf("failed to open .env file: %w", err)
	}
	defer file.Close()

	// Parse the file line by line
	// 逐行解析文件
	var buffer bytes.Buffer
	scanner := bufio.NewScanner(file)
	updatedKeys := make(map[string]bool)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments, but preserve them
		// 跳过空行和注释，但保留它们
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			buffer.WriteString(line + "\n")
			continue
		}

		// Parse key=value
		// 解析 key=value
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			buffer.WriteString(line + "\n")
			continue
		}

		key := strings.TrimSpace(parts[0])

		// Check if this key needs to be updated
		// 检查此键是否需要更新
		if newValue, exists := updates[key]; exists {
			buffer.WriteString(fmt.Sprintf("%s=%s\n", key, newValue))
			updatedKeys[key] = true
		} else {
			buffer.WriteString(line + "\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read .env file: %w", err)
	}

	// Append any new keys that weren't in the original file
	// 添加原文件中不存在的新键
	for key, value := range updates {
		if !updatedKeys[key] {
			buffer.WriteString(fmt.Sprintf("%s=%s\n", key, value))
		}
	}

	// Write the updated content back to the file
	// 将更新后的内容写回文件
	err = os.WriteFile(envPath, buffer.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("failed to write .env file: %w", err)
	}

	return nil
}

// createNewEnvFile creates a new .env file with the given key-value pairs
// createNewEnvFile 使用给定的键值对创建新的 .env 文件
func createNewEnvFile(envPath string, data map[string]string) error {
	var buffer bytes.Buffer

	buffer.WriteString("# Auto-generated .env file\n")
	buffer.WriteString("# 自动生成的 .env 文件\n\n")

	for key, value := range data {
		buffer.WriteString(fmt.Sprintf("%s=%s\n", key, value))
	}

	err := os.WriteFile(envPath, buffer.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("failed to create .env file: %w", err)
	}

	return nil
}
