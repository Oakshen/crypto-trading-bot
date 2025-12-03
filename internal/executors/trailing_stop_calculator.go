package executors

import (
	"fmt"
	"math"
	"strings"

	"github.com/oak/crypto-trading-bot/internal/logger"
)

// TrailingStopConfig represents trailing stop configuration for a symbol
// TrailingStopConfig 表示某个交易对的追踪止损配置
type TrailingStopConfig struct {
	// Initial stop-loss parameters
	// 初始止损参数
	InitialATRPeriod     int     // ATR period for initial stop, default 14 (Wilder's standard) / 初始止损的 ATR 周期，默认 14（标准 Wilder 周期）
	InitialATRMultiplier float64 // ATR multiplier for initial stop, default 2.5 / 初始止损的 ATR 倍数，默认 2.5

	// Trailing stop parameters
	// 追踪止损参数
	TrailingATRPeriod     int     // ATR period for trailing stop, default 14 (Wilder's standard) / 追踪止损的 ATR 周期，默认 14（标准 Wilder 周期）
	TrailingATRMultiplier float64 // ATR multiplier for trailing stop, default 2.0 / 追踪止损的 ATR 倍数，默认 2.0

	// Update control
	// 更新控制
	UpdateThreshold float64 // Update threshold in percentage, default 1.0 / 更新阈值（百分比），默认 1.0
	MinStopDistance float64 // Minimum stop distance in percentage, default 1.5 / 最小止损距离（百分比），默认 1.5
	MaxStopDistance float64 // Maximum stop distance in percentage, default 8.0 / 最大止损距离（百分比），默认 8.0
}

// TrailingStopCalculator calculates trailing stop prices locally
// TrailingStopCalculator 本地计算追踪止损价格
//
// This calculator replaces LLM-based stop-loss calculation with deterministic formulas.
// 此计算器使用确定性公式替代基于 LLM 的止损计算。
//
// ATR Source: Calculated from longer timeframe (e.g., 4h) with configurable period (TRAILING_STOP_ATR_PERIOD in .env, default 7)
// ATR 来源：从长期时间周期（如 4h）计算，周期可配置（.env 中的 TRAILING_STOP_ATR_PERIOD，默认 7）
//
// Formulas:
// 公式：
//   - Initial stop (long): entry_price - 2.5 × ATR(from longer timeframe)
//   - Initial stop (short): entry_price + 2.5 × ATR(from longer timeframe)
//   - Trailing stop (long): highest_price - 2.0-3.0 × ATR(from longer timeframe)
//   - Trailing stop (short): lowest_price + 2.0-3.0 × ATR(from longer timeframe)
//
// Rules:
// 规则：
//   - Stop-loss can only move in favorable direction (up for long, down for short)
//   - 止损只能朝有利方向移动（多仓向上，空仓向下）
//   - Update only if change exceeds threshold (default 1%)
//   - 仅当变化超过阈值时才更新（默认 1%）
type TrailingStopCalculator struct {
	configs map[string]TrailingStopConfig // Symbol-specific configs / 币种特定配置
	logger  *logger.ColorLogger           // Logger / 日志记录器
}

// NewTrailingStopCalculator creates a new trailing stop calculator
// NewTrailingStopCalculator 创建新的追踪止损计算器
func NewTrailingStopCalculator(log *logger.ColorLogger) *TrailingStopCalculator {
	return &TrailingStopCalculator{
		configs: getDefaultConfigs(),
		logger:  log,
	}
}

// getDefaultConfigs returns default configurations for different symbols
// getDefaultConfigs 返回不同币种的默认配置
//
// ⚙️ THIS IS THE CENTRAL CONFIGURATION FOR TRAILING STOP PARAMETERS
// ⚙️ 这是追踪止损参数的中心配置
//
// Parameter descriptions:
// 参数说明：
//   - InitialATRMultiplier:  Initial stop distance = entry_price ± (multiplier × ATR)
//     初始止损距离 = 入场价 ± (倍数 × ATR)
//   - TrailingATRMultiplier: Trailing stop distance = highest/lowest_price ± (multiplier × ATR)
//     追踪止损距离 = 最高/最低价 ± (倍数 × ATR)
//   - UpdateThreshold:       Only update if stop price change >= threshold%
//     仅当止损价格变化 >= 阈值% 时才更新（避免频繁调整）
//   - MinStopDistance:       Minimum allowed stop distance from entry (prevents too tight stops)
//     允许的最小止损距离（防止止损过紧）
//   - MaxStopDistance:       Maximum allowed stop distance from entry (prevents excessive risk)
//     允许的最大止损距离（防止风险过大）
//
// You can customize parameters for each symbol based on their volatility characteristics.
// 你可以根据每个币种的波动性特征自定义参数。
func getDefaultConfigs() map[string]TrailingStopConfig {
	// Default configuration (used as fallback for undefined symbols)
	// 默认配置（用于未定义的币种）
	defaultConfig := TrailingStopConfig{
		InitialATRPeriod:      7, // 使用 ATR(7) - 标准 Wilder 周期
		InitialATRMultiplier:  3,
		TrailingATRPeriod:     7, // 使用 ATR(7) - 标准 Wilder 周期
		TrailingATRMultiplier: 3,
		UpdateThreshold:       0.3, // 0.3% - update only if change exceeds this
		MinStopDistance:       0.5, // 0.5% - minimum stop distance from entry
		MaxStopDistance:       5.0, // 5.0% - maximum stop distance from entry
	}

	configs := make(map[string]TrailingStopConfig)

	// BTC - Lower volatility, moderate stop distance
	// BTC - 波动较小，止损距离适中
	configs["BTCUSDT"] = TrailingStopConfig{
		InitialATRPeriod:      7,
		InitialATRMultiplier:  3.5,
		TrailingATRPeriod:     7,
		TrailingATRMultiplier: 2.8,
		UpdateThreshold:       0.3,
		MinStopDistance:       0.5,
		MaxStopDistance:       6.0,
	}

	// ETH - Similar to BTC
	// ETH - 类似 BTC
	configs["ETHUSDT"] = TrailingStopConfig{
		InitialATRPeriod:      7,
		InitialATRMultiplier:  3.5,
		TrailingATRPeriod:     7,
		TrailingATRMultiplier: 2.7,
		UpdateThreshold:       0.3,
		MinStopDistance:       0.5,
		MaxStopDistance:       6.0,
	}

	// SOL - Higher volatility, wider stop distance
	// SOL - 波动较大，止损距离稍宽
	configs["SOLUSDT"] = TrailingStopConfig{
		InitialATRPeriod:      7,
		InitialATRMultiplier:  3.5,
		TrailingATRPeriod:     7,
		TrailingATRMultiplier: 2.5, // Slightly wider / 稍微宽松一点
		UpdateThreshold:       0.3,
		MinStopDistance:       0.5,
		MaxStopDistance:       8.0,
	}

	// BNB - Moderate volatility
	// BNB - 中等波动
	configs["BNBUSDT"] = TrailingStopConfig{
		InitialATRPeriod:      7,
		InitialATRMultiplier:  3.5,
		TrailingATRPeriod:     7,
		TrailingATRMultiplier: 2.2,
		UpdateThreshold:       0.3,
		MinStopDistance:       0.5,
		MaxStopDistance:       7.0,
	}

	// XRP - Higher volatility
	// XRP - 波动较大
	configs["XRPUSDT"] = TrailingStopConfig{
		InitialATRPeriod:      7,
		InitialATRMultiplier:  3.5,
		TrailingATRPeriod:     7,
		TrailingATRMultiplier: 3.5,
		UpdateThreshold:       0.3,
		MinStopDistance:       0.5,
		MaxStopDistance:       8.0,
	}

	// Add more symbols as needed
	// 根据需要添加更多币种
	// You can adjust parameters based on actual trading performance
	// 你可以根据实际交易表现调整参数

	// Use default config for any undefined symbols
	// 未定义的币种使用默认配置
	configs["DEFAULT"] = defaultConfig

	return configs
}

// GetConfig returns configuration for a specific symbol
// GetConfig 返回指定币种的配置
func (calc *TrailingStopCalculator) GetConfig(symbol string) TrailingStopConfig {
	// Normalize symbol (remove slash)
	// 标准化符号（去除斜杠）
	normalizedSymbol := strings.ReplaceAll(symbol, "/", "")
	normalizedSymbol = strings.ToUpper(normalizedSymbol)

	if config, exists := calc.configs[normalizedSymbol]; exists {
		return config
	}

	// Return default config if symbol not found
	// 如果未找到币种配置，返回默认配置
	if calc.logger != nil {
		calc.logger.Warning(fmt.Sprintf("⚠️  未找到 %s 的追踪止损配置，使用默认参数", symbol))
	}
	return calc.configs["DEFAULT"]
}

// CalculateInitialStop calculates initial stop-loss price when opening a position
// CalculateInitialStop 计算开仓时的初始止损价格
//
// Parameters:
// 参数：
//   - symbol: Trading symbol (e.g., "BTCUSDT", "BTC/USDT")
//   - entryPrice: Entry price
//   - atr: ATR (Average True Range) value
//   - side: "long" or "short"
//
// Returns:
// 返回：
//   - Initial stop-loss price
//   - 初始止损价格
func (calc *TrailingStopCalculator) CalculateInitialStop(
	symbol string,
	entryPrice float64,
	atr float64,
	side string,
) float64 {
	config := calc.GetConfig(symbol)

	// Calculate stop distance
	// 计算止损距离
	stopDistance := config.InitialATRMultiplier * atr

	var stopPrice float64
	if side == "long" {
		// Long: stop below entry price
		// 多仓：止损价低于入场价
		stopPrice = entryPrice - stopDistance
	} else {
		// Short: stop above entry price
		// 空仓：止损价高于入场价
		stopPrice = entryPrice + stopDistance
	}

	if calc.logger != nil {
		calc.logger.Info(fmt.Sprintf("【%s】计算初始止损: 入场价=%.2f, ATR=%.2f, 倍数=%.1f, 止损价=%.2f",
			symbol, entryPrice, atr, config.InitialATRMultiplier, stopPrice))
	}

	return stopPrice
}

// CalculateTrailingStop calculates trailing stop price for an existing position
// CalculateTrailingStop 计算现有持仓的追踪止损价格
//
// Parameters:
// 参数：
//   - symbol: Trading symbol
//   - highestPrice: Highest price since entry (for long) or lowest price (for short)
//   - atr: Current ATR value
//   - side: "long" or "short"
//
// Returns:
// 返回：
//   - Trailing stop price
//   - 追踪止损价格
//
// Formula:
// 公式：
//   - Long: stop_price = highest_price - (multiplier × ATR)
//   - Short: stop_price = lowest_price + (multiplier × ATR)
func (calc *TrailingStopCalculator) CalculateTrailingStop(
	symbol string,
	highestPrice float64,
	atr float64,
	side string,
) float64 {
	config := calc.GetConfig(symbol)

	// Calculate stop distance
	// 计算止损距离
	stopDistance := config.TrailingATRMultiplier * atr

	var stopPrice float64
	if side == "long" {
		// Long: stop = highest_price - N×ATR
		// 多仓：止损价 = 最高价 - N×ATR
		stopPrice = highestPrice - stopDistance
	} else {
		// Short: stop = lowest_price + N×ATR
		// 空仓：止损价 = 最低价 + N×ATR
		// Note: for short positions, highestPrice field stores the lowest price
		// 注意：空仓的 highestPrice 字段存储的是最低价
		stopPrice = highestPrice + stopDistance
	}

	if calc.logger != nil {
		priceType := "最高价"
		if side == "short" {
			priceType = "最低价"
		}
		calc.logger.Info(fmt.Sprintf("【%s】计算追踪止损: %s=%.2f, ATR=%.2f, 倍数=%.1f, 止损价=%.2f",
			symbol, priceType, highestPrice, atr, config.TrailingATRMultiplier, stopPrice))
	}

	return stopPrice
}

// IsValidUpdate checks if stop-loss update moves in favorable direction
// IsValidUpdate 检查止损更新是否朝有利方向移动
//
// Rules:
// 规则：
//   - Long: new stop must be higher than old stop (moving up)
//   - 多仓：新止损必须高于旧止损（向上移动）
//   - Short: new stop must be lower than old stop (moving down)
//   - 空仓：新止损必须低于旧止损（向下移动）
//
// Parameters:
// 参数：
//   - side: "long" or "short"
//   - oldStopLoss: Current stop-loss price
//   - newStopLoss: Proposed new stop-loss price
//
// Returns:
// 返回：
//   - true if update is valid (favorable direction)
//   - true 表示更新有效（朝有利方向）
func (calc *TrailingStopCalculator) IsValidUpdate(
	side string,
	oldStopLoss float64,
	newStopLoss float64,
) bool {
	if side == "long" {
		// Long: new stop must be higher (moving up to protect profit)
		// 多仓：新止损必须更高（向上移动以保护利润）
		return newStopLoss > oldStopLoss
	} else {
		// Short: new stop must be lower (moving down to protect profit)
		// 空仓：新止损必须更低（向下移动以保护利润）
		return newStopLoss < oldStopLoss
	}
}

// ShouldUpdate checks if stop-loss change exceeds threshold
// ShouldUpdate 检查止损变化是否超过阈值
//
// This prevents frequent updates for small price movements.
// 这可以防止因小幅价格波动而频繁更新止损单。
//
// Parameters:
// 参数：
//   - symbol: Trading symbol
//   - oldStopLoss: Current stop-loss price
//   - newStopLoss: Proposed new stop-loss price
//
// Returns:
// 返回：
//   - true if change percentage exceeds threshold
//   - true 表示变化百分比超过阈值
func (calc *TrailingStopCalculator) ShouldUpdate(
	symbol string,
	oldStopLoss float64,
	newStopLoss float64,
) bool {
	config := calc.GetConfig(symbol)

	// Calculate change percentage
	// 计算变化百分比
	changePercent := math.Abs((newStopLoss-oldStopLoss)/oldStopLoss) * 100

	return changePercent >= config.UpdateThreshold
}

// ValidateStopDistance checks if stop distance is within reasonable range
// ValidateStopDistance 检查止损距离是否在合理范围内
//
// This prevents setting stops too tight (frequently triggered) or too wide (excessive risk).
// 这可以防止止损设置过紧（频繁触发）或过宽（风险过大）。
//
// Parameters:
// 参数：
//   - symbol: Trading symbol
//   - entryPrice: Entry price (or current price for validation)
//   - stopPrice: Proposed stop-loss price
//   - side: "long" or "short"
//
// Returns:
// 返回：
//   - true if stop distance is within min/max range
//   - true 表示止损距离在最小/最大范围内
func (calc *TrailingStopCalculator) ValidateStopDistance(
	symbol string,
	entryPrice float64,
	stopPrice float64,
	side string,
) bool {
	config := calc.GetConfig(symbol)

	// Calculate stop distance percentage
	// 计算止损距离百分比
	var distancePercent float64
	if side == "long" {
		distancePercent = ((entryPrice - stopPrice) / entryPrice) * 100
	} else {
		distancePercent = ((stopPrice - entryPrice) / entryPrice) * 100
	}

	// Check if within min/max range
	// 检查是否在最小/最大范围内
	isValid := distancePercent >= config.MinStopDistance && distancePercent <= config.MaxStopDistance

	if !isValid && calc.logger != nil {
		calc.logger.Warning(fmt.Sprintf("⚠️【%s】止损距离 %.2f%% 超出合理范围 [%.1f%%, %.1f%%]",
			symbol, distancePercent, config.MinStopDistance, config.MaxStopDistance))
	}

	return isValid
}
