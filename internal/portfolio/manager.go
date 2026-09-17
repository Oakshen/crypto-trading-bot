package portfolio

import (
	"context"
	"fmt"

	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/executors"
	"github.com/oak/crypto-trading-bot/internal/logger"
)

// PositionInfo represents information about a position for a symbol
// PositionInfo 表示某个交易对的仓位信息
type PositionInfo struct {
	Symbol           string                // 交易对 / Trading pair
	Position         *executors.Position   // 当前持仓 / Current position
	AllocatedBalance float64               // 分配的余额 / Allocated balance
	MaxPositionSize  float64               // 最大仓位大小 / Max position size
	Action           executors.TradeAction // 待执行动作 / Pending action
}

// PortfolioManager manages multiple trading pairs and position allocation
// PortfolioManager 管理多个交易对和仓位分配
type PortfolioManager struct {
	config           *config.Config
	executor         *executors.BinanceExecutor
	logger           *logger.ColorLogger
	totalBalance     float64                  // 总余额 / Total balance
	availableBalance float64                  // 可用余额 / Available balance
	positions        map[string]*PositionInfo // 各交易对的仓位 / Positions for each pair
	maxTotalRisk     float64                  // 最大总风险敞口 / Max total risk exposure
}

// NewPortfolioManager creates a new PortfolioManager
// NewPortfolioManager 创建新的仓位管理器
func NewPortfolioManager(cfg *config.Config, executor *executors.BinanceExecutor, log *logger.ColorLogger) *PortfolioManager {
	return &PortfolioManager{
		config:       cfg,
		executor:     executor,
		logger:       log,
		positions:    make(map[string]*PositionInfo),
		maxTotalRisk: 0.30, // 最大总风险敞口 30% / Max total risk exposure 30%
	}
}

// UpdateBalance updates the account balance information
// UpdateBalance 更新账户余额信息
func (pm *PortfolioManager) UpdateBalance(ctx context.Context) error {
	balance, err := pm.executor.GetAccountBalance(ctx)
	if err != nil {
		return fmt.Errorf("failed to get account balance: %w", err)
	}

	pm.totalBalance = balance.WalletBalance
	pm.availableBalance = balance.AvailableBalance
	// Balance log removed to reduce verbosity (logged when saving balance snapshot)
	// 移除余额日志以减少冗余（在保存余额快照时会打印）

	return nil
}

// UpdatePosition updates position information for a symbol
// UpdatePosition 更新某个交易对的仓位信息
func (pm *PortfolioManager) UpdatePosition(ctx context.Context, symbol string) error {
	position, err := pm.executor.GetCurrentPosition(ctx, symbol)
	if err != nil {
		return fmt.Errorf("failed to get position for %s: %w", symbol, err)
	}

	if pm.positions[symbol] == nil {
		pm.positions[symbol] = &PositionInfo{
			Symbol: symbol,
		}
	}

	pm.positions[symbol].Position = position
	return nil
}

// CheckRiskLimits checks if adding a new position would exceed risk limits
// CheckRiskLimits 检查新增仓位是否超过风险限制
func (pm *PortfolioManager) CheckRiskLimits(symbol string, positionSize float64, currentPrice float64) error {
	// Calculate total risk exposure
	// 计算总风险敞口
	totalExposure := 0.0
	for _, posInfo := range pm.positions {
		if posInfo.Position != nil {
			exposure := posInfo.Position.Size * posInfo.Position.EntryPrice
			totalExposure += exposure
		}
	}

	// Add proposed position
	// 加上拟开仓位
	proposedExposure := positionSize * currentPrice
	totalExposure += proposedExposure

	// Check against total balance
	// 检查是否超过总余额限制
	leverage := float64(pm.config.BinanceLeverage)
	maxAllowedExposure := pm.totalBalance * pm.maxTotalRisk * leverage

	if totalExposure > maxAllowedExposure {
		return fmt.Errorf("超过最大风险敞口限制: 当前 %.2f USDT / 限制 %.2f USDT",
			totalExposure, maxAllowedExposure)
	}

	pm.logger.Success(fmt.Sprintf("✅ 风险检查通过: 总敞口 %.2f / %.2f USDT",
		totalExposure, maxAllowedExposure))

	return nil
}

// GetPortfolioSummary returns a summary of all positions
// GetPortfolioSummary 返回所有仓位的摘要
func (pm *PortfolioManager) GetPortfolioSummary() string {
	summary := fmt.Sprintf("\n=== 投资组合摘要 ===\n")
	summary += fmt.Sprintf("总余额: %.2f USDT\n", pm.totalBalance)
	summary += fmt.Sprintf("可用余额: %.2f USDT\n", pm.availableBalance)
	summary += fmt.Sprintf("已用保证金: %.2f USDT\n\n", pm.totalBalance-pm.availableBalance)

	if len(pm.positions) == 0 {
		summary += "当前无持仓\n"
		return summary
	}

	totalPnL := 0.0
	for symbol, posInfo := range pm.positions {
		if posInfo.Position != nil && posInfo.Position.Size > 0 {
			summary += fmt.Sprintf("【%s】\n", symbol)
			summary += fmt.Sprintf("  方向: %s\n", posInfo.Position.Side)
			summary += fmt.Sprintf("  数量: %.4f\n", posInfo.Position.Size)
			summary += fmt.Sprintf("  入场价: $%.2f\n", posInfo.Position.EntryPrice)
			summary += fmt.Sprintf("  未实现盈亏: %+.2f USDT\n\n", posInfo.Position.UnrealizedPnL)
			totalPnL += posInfo.Position.UnrealizedPnL
		}
	}

	summary += fmt.Sprintf("总未实现盈亏: %+.2f USDT\n", totalPnL)
	return summary
}

// BalancePortfolio suggests position adjustments to balance the portfolio
// BalancePortfolio 建议调整仓位以平衡投资组合
func (pm *PortfolioManager) BalancePortfolio() map[string]string {
	suggestions := make(map[string]string)

	// Check for concentrated risk
	// 检查风险集中度
	totalExposure := 0.0
	exposures := make(map[string]float64)

	for symbol, posInfo := range pm.positions {
		if posInfo.Position != nil && posInfo.Position.Size > 0 {
			exposure := posInfo.Position.Size * posInfo.Position.EntryPrice
			exposures[symbol] = exposure
			totalExposure += exposure
		}
	}

	// Suggest rebalancing if any single position exceeds 50% of total
	// 如果任何单个仓位超过总仓位的 50%，建议重新平衡
	for symbol, exposure := range exposures {
		ratio := exposure / totalExposure
		if ratio > 0.5 {
			suggestions[symbol] = fmt.Sprintf("⚠️ 仓位过于集中 (%.1f%%)，建议减仓", ratio*100)
		}
	}

	return suggestions
}

// Helper function to parse float from string
// 辅助函数：从字符串解析浮点数
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// RebalanceAllocation rebalances position allocation across multiple symbols
// RebalanceAllocation 在多个交易对之间重新分配仓位
func (pm *PortfolioManager) RebalanceAllocation(symbols []string) map[string]float64 {
	// Simple equal weight allocation
	// 简单的等权重分配
	allocation := make(map[string]float64)
	weightPerSymbol := 1.0 / float64(len(symbols))
	allocatedPerSymbol := pm.availableBalance * weightPerSymbol * pm.maxTotalRisk

	for _, symbol := range symbols {
		allocation[symbol] = allocatedPerSymbol
	}

	pm.logger.Info(fmt.Sprintf("仓位分配: 每个交易对分配 %.2f USDT (%.1f%%)",
		allocatedPerSymbol, weightPerSymbol*100))

	return allocation
}

// GetTotalBalance returns the total balance
// GetTotalBalance 返回总余额
func (pm *PortfolioManager) GetTotalBalance() float64 {
	return pm.totalBalance
}

// GetAvailableBalance returns the available balance
// GetAvailableBalance 返回可用余额
func (pm *PortfolioManager) GetAvailableBalance() float64 {
	return pm.availableBalance
}

// GetTotalUnrealizedPnL calculates total unrealized PnL across all positions
// GetTotalUnrealizedPnL 计算所有持仓的总未实现盈亏
func (pm *PortfolioManager) GetTotalUnrealizedPnL() float64 {
	totalPnL := 0.0
	for _, posInfo := range pm.positions {
		if posInfo.Position != nil && posInfo.Position.Size > 0 {
			totalPnL += posInfo.Position.UnrealizedPnL
		}
	}
	return totalPnL
}

// GetPositionCount returns the number of open positions
// GetPositionCount 返回开仓数量
func (pm *PortfolioManager) GetPositionCount() int {
	count := 0
	for _, posInfo := range pm.positions {
		if posInfo.Position != nil && posInfo.Position.Size > 0 {
			count++
		}
	}
	return count
}

// GetPosition returns the position for a specific symbol
// GetPosition 返回特定交易对的持仓信息
func (pm *PortfolioManager) GetPosition(symbol string) *executors.Position {
	if posInfo, ok := pm.positions[symbol]; ok {
		return posInfo.Position
	}
	return nil
}
