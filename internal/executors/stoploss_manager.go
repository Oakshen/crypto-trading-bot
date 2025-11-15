package executors

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/logger"
	"github.com/oak/crypto-trading-bot/internal/storage"
)

// StopLossManager manages stop-loss for all active positions
// StopLossManager 管理所有活跃持仓的止损
//
// Architecture: Server-side stop-loss strategy (Fixed stop-loss)
// 架构：服务器端止损策略（固定止损）
//
// Responsibilities:
// 职责：
//  1. Position lifecycle management (register, remove, query)
//     持仓生命周期管理（注册、移除、查询）
//  2. Binance stop-loss order placement and cancellation
//     币安止损单下单和取消
//  3. Position data storage and retrieval
//     持仓数据存储和检索
//
// Note: Local price monitoring is DISABLED. Stop-loss execution relies entirely on
// Binance server-side STOP_MARKET orders, which provide:
// 注意：本地价格监控已禁用。止损执行完全依赖币安服务器端 STOP_MARKET 订单，优势：
//   - 24/7 server-side monitoring (no local uptime dependency)
//     24/7 服务器端监控（不依赖本地程序运行）
//   - Millisecond-level trigger speed (vs 10s polling)
//     毫秒级触发速度（相比 10 秒轮询）
//   - Resilience to local program crashes/network issues
//     对本地程序崩溃/网络问题有弹性
//   - No duplicate execution risk
//     无重复执行风险
type StopLossManager struct {
	positions map[string]*Position // symbol -> Position
	executor  *BinanceExecutor     // 执行器 / Executor
	config    *config.Config       // 配置 / Config
	logger    *logger.ColorLogger  // 日志 / Logger
	storage   *storage.Storage     // 数据库 / Database
	mu        sync.RWMutex         // 读写锁 / RW mutex
	ctx       context.Context      // 上下文 / Context
	cancel    context.CancelFunc   // 取消函数 / Cancel function
}

// NewStopLossManager creates a new StopLossManager
// NewStopLossManager 创建新的止损管理器
func NewStopLossManager(cfg *config.Config, executor *BinanceExecutor, log *logger.ColorLogger, db *storage.Storage) *StopLossManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &StopLossManager{
		positions: make(map[string]*Position),
		executor:  executor,
		config:    cfg,
		logger:    log,
		storage:   db,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// RegisterPosition registers a new position for stop-loss management
// RegisterPosition 注册新持仓进行止损管理
func (sm *StopLossManager) RegisterPosition(pos *Position) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Normalize symbol to Binance format (BTCUSDT instead of BTC/USDT)
	// 统一符号格式为币安格式（BTCUSDT 而不是 BTC/USDT）
	// This prevents duplicate position tracking for the same asset
	// 防止同一资产被重复跟踪
	normalizedSymbol := sm.config.GetBinanceSymbolFor(pos.Symbol)
	pos.Symbol = normalizedSymbol

	pos.HighestPrice = pos.EntryPrice // 初始化最高价/最低价 / Initialize highest/lowest
	pos.CurrentPrice = pos.EntryPrice
	pos.StopLossType = "fixed" // LLM 驱动的固定止损 / LLM-driven fixed stop

	sm.positions[normalizedSymbol] = pos
	sm.logger.Success(fmt.Sprintf("【%s】持仓已注册，入场价: %.2f, 初始止损: %.2f, 当前止损: %.2f",
		normalizedSymbol, pos.EntryPrice, pos.InitialStopLoss, pos.CurrentStopLoss))
}

// RemovePosition removes a position from management
// RemovePosition 从管理中移除持仓
func (sm *StopLossManager) RemovePosition(symbol string) {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.positions, normalizedSymbol)
	sm.logger.Info(fmt.Sprintf("【%s】持仓已移除", symbol))
}

// ClosePosition closes a position completely: cancels stop-loss order, removes from memory, and updates database
// ClosePosition 完整关闭持仓：取消止损单、从内存移除、更新数据库
func (sm *StopLossManager) ClosePosition(ctx context.Context, symbol string, closePrice float64, closeReason string, realizedPnL float64) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	pos, exists := sm.positions[normalizedSymbol]
	sm.mu.Unlock()

	if !exists {
		sm.logger.Warning(fmt.Sprintf("⚠️  %s 持仓不存在，无需关闭", symbol))
		return nil
	}

	sm.logger.Info(fmt.Sprintf("【%s】正在关闭持仓...", symbol))

	// Step 1: Cancel Binance stop-loss order
	// 步骤 1：取消币安止损单
	if pos.StopLossOrderID != "" {
		if err := sm.cancelStopLossOrder(ctx, pos); err != nil {
			sm.logger.Warning(fmt.Sprintf("⚠️  取消 %s 止损单失败: %v（继续关闭流程）", symbol, err))
		} else {
			sm.logger.Success(fmt.Sprintf("✅ %s 止损单已取消", symbol))
		}
	}

	// Step 2: Remove from memory
	// 步骤 2：从内存移除
	sm.mu.Lock()
	delete(sm.positions, normalizedSymbol)
	sm.mu.Unlock()
	sm.logger.Info(fmt.Sprintf("✅ %s 已从止损管理器移除", symbol))

	// Step 3: Update database status
	// 步骤 3：更新数据库状态
	if sm.storage != nil {
		// Get position record from database
		// 从数据库获取持仓记录
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err != nil {
			sm.logger.Warning(fmt.Sprintf("⚠️  获取 %s 持仓记录失败: %v（跳过数据库更新）", symbol, err))
		} else if posRecord != nil {
			// Update position record
			// 更新持仓记录
			now := time.Now()
			posRecord.Closed = true
			posRecord.CloseTime = &now
			posRecord.ClosePrice = closePrice
			posRecord.CloseReason = closeReason
			posRecord.RealizedPnL = realizedPnL

			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新 %s 数据库状态失败: %v", symbol, err))
			} else {
				sm.logger.Success(fmt.Sprintf("✅ %s 数据库状态已更新为已关闭", symbol))
			}
		}
	}

	sm.logger.Success(fmt.Sprintf("✅【%s】持仓完全关闭（止损单已取消，内存已清理，数据库已更新）", symbol))
	return nil
}

// PlaceInitialStopLoss places initial stop-loss order for a position
// PlaceInitialStopLoss 为持仓下初始止损单
func (sm *StopLossManager) PlaceInitialStopLoss(ctx context.Context, pos *Position) error {
	err := sm.placeStopLossOrder(ctx, pos, pos.InitialStopLoss)
	if err != nil {
		return err
	}

	// Sync stop-loss order ID to database
	// 同步止损单 ID 到数据库
	if sm.storage != nil && pos.StopLossOrderID != "" {
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err == nil && posRecord != nil {
			posRecord.StopLossOrderID = pos.StopLossOrderID
			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新数据库止损单 ID 失败: %v", err))
			} else {
				sm.logger.Info(fmt.Sprintf("✓ 数据库已同步止损单 ID: %s", pos.StopLossOrderID))
			}
		}
	}

	return nil
}

// GetPosition gets a position by symbol
// GetPosition 根据交易对获取持仓
func (sm *StopLossManager) GetPosition(symbol string) *Position {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.positions[normalizedSymbol]
}

// AddToPosition updates stop-loss after adding to position
// AddToPosition 加仓后更新止损单
func (sm *StopLossManager) AddToPosition(ctx context.Context, pos *Position, newStopLoss float64) error {
	// Normalize symbol
	// 标准化符号
	normalizedSymbol := sm.config.GetBinanceSymbolFor(pos.Symbol)

	sm.mu.Lock()
	managedPos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return fmt.Errorf("持仓 %s 不存在于止损管理器中", pos.Symbol)
	}
	sm.mu.Unlock()

	sm.logger.Info(fmt.Sprintf("【%s】开始更新加仓后的止损单...", normalizedSymbol))

	// Cancel old stop-loss order
	// 取消旧止损单
	if managedPos.StopLossOrderID != "" {
		sm.logger.Info(fmt.Sprintf("  → 取消旧止损单: %s", managedPos.StopLossOrderID))
		if err := sm.cancelStopLossOrder(ctx, managedPos); err != nil {
			sm.logger.Error(fmt.Sprintf("❌ 取消旧止损单失败: %v", err))
			return fmt.Errorf("无法取消旧止损单（订单ID: %s）: %w", managedPos.StopLossOrderID, err)
		}
	}

	// Update managed position with new values from added position
	// 用加仓后的新值更新管理的持仓
	managedPos.Quantity = pos.Quantity
	managedPos.Size = pos.Size
	managedPos.AverageEntryPrice = pos.AverageEntryPrice
	managedPos.TotalEntries = pos.TotalEntries
	managedPos.TotalInvestment = pos.TotalInvestment

	// Determine new stop-loss price
	// 确定新止损价格
	stopLossPrice := newStopLoss
	if stopLossPrice == 0 {
		// If LLM didn't provide new stop-loss, protect cost (average entry price)
		// 如果 LLM 没有提供新止损，则保护成本价（平均入场价）
		if managedPos.Side == "long" {
			stopLossPrice = managedPos.AverageEntryPrice * 0.975 // -2.5%
		} else {
			stopLossPrice = managedPos.AverageEntryPrice * 1.025 // +2.5%
		}
		sm.logger.Info(fmt.Sprintf("  → LLM 未提供新止损，使用成本保护止损: %.2f", stopLossPrice))
	} else {
		sm.logger.Info(fmt.Sprintf("  → 使用 LLM 提供的新止损: %.2f", stopLossPrice))
	}

	// Place new stop-loss order with updated quantity
	// 下新止损单（使用更新后的数量）
	sm.logger.Info(fmt.Sprintf("  → 下新止损单: 数量 %.4f, 止损价 %.2f", managedPos.Quantity, stopLossPrice))
	if err := sm.placeStopLossOrder(ctx, managedPos, stopLossPrice); err != nil {
		return fmt.Errorf("下新止损单失败: %w", err)
	}

	// Update stop-loss price
	// 更新止损价格
	managedPos.CurrentStopLoss = stopLossPrice

	sm.logger.Success(fmt.Sprintf("✅【%s】加仓后止损单已更新", normalizedSymbol))
	sm.logger.Info(fmt.Sprintf("   新持仓: %.4f @ %.2f (平均)", managedPos.Quantity, managedPos.AverageEntryPrice))
	sm.logger.Info(fmt.Sprintf("   新止损: %.2f (订单ID: %s)", stopLossPrice, managedPos.StopLossOrderID))

	// Sync to database
	// 同步到数据库
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(managedPos.ID)
		if err == nil && posRecord != nil {
			posRecord.Quantity = managedPos.Quantity
			posRecord.AverageEntryPrice = managedPos.AverageEntryPrice
			posRecord.TotalEntries = managedPos.TotalEntries
			posRecord.TotalInvestment = managedPos.TotalInvestment
			posRecord.CurrentStopLoss = managedPos.CurrentStopLoss
			posRecord.StopLossOrderID = managedPos.StopLossOrderID

			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新数据库失败: %v", err))
			} else {
				sm.logger.Info("✓ 数据库已同步")
			}
		}
	}

	return nil
}

// UpdateStopLoss updates stop-loss price for a position (called by LLM every 15 minutes)
// UpdateStopLoss 更新持仓的止损价格（每 15 分钟由 LLM 调用）
func (sm *StopLossManager) UpdateStopLoss(ctx context.Context, symbol string, newStopLoss float64, reason string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return fmt.Errorf("持仓 %s 不存在", symbol)
	}
	sm.mu.Unlock()

	oldStop := pos.CurrentStopLoss

	// Validate stop-loss movement (only allow favorable direction)
	// 验证止损移动（只允许朝有利方向移动）
	if pos.Side == "long" && newStopLoss < oldStop {
		sm.logger.Warning(fmt.Sprintf("【%s】⚠️ LLM 建议降低多仓止损 (%.2f → %.2f)，拒绝（止损只能向上移动）",
			pos.Symbol, oldStop, newStopLoss))
		return fmt.Errorf("多仓止损只能向上移动")
	}
	if pos.Side == "short" && newStopLoss > oldStop {
		sm.logger.Warning(fmt.Sprintf("【%s】⚠️ LLM 建议提高空仓止损 (%.2f → %.2f)，拒绝（止损只能向下移动）",
			pos.Symbol, oldStop, newStopLoss))
		return fmt.Errorf("空仓止损只能向下移动")
	}

	// Check if change is significant enough (threshold from config)
	// 检查变化是否足够大（阈值从配置读取）
	changePercent := math.Abs((newStopLoss-oldStop)/oldStop) * 100
	threshold := sm.config.StopLossScopeThreshold
	if changePercent < threshold {
		sm.logger.Info(fmt.Sprintf("【%s】💡 止损价格变化较小 (%.2f → %.2f, 变化 %.2f%% < 阈值 %.1f%%)，跳过更新以避免频繁调整",
			pos.Symbol, oldStop, newStopLoss, changePercent, threshold))
		return nil
	}

	// Record history
	// 记录历史
	pos.AddStopLossEvent(oldStop, newStopLoss, reason, "llm")

	// Cancel old stop-loss order if exists
	// 取消旧的止损单（如果存在）
	// CRITICAL: Old order MUST be cancelled before placing new one to avoid duplicate orders
	// 关键：必须先取消旧订单再下新订单，避免出现重复止损单
	if pos.StopLossOrderID != "" {
		if err := sm.cancelStopLossOrder(ctx, pos); err != nil {
			sm.logger.Error(fmt.Sprintf("❌ 取消旧止损单失败: %v", err))
			return fmt.Errorf("无法取消旧止损单（订单ID: %s）: %w", pos.StopLossOrderID, err)
		}
	}

	// Place new stop-loss order
	// 下新的止损单
	if err := sm.placeStopLossOrder(ctx, pos, newStopLoss); err != nil {
		return fmt.Errorf("下止损单失败: %w", err)
	}

	pos.CurrentStopLoss = newStopLoss
	sm.logger.Success(fmt.Sprintf("【%s】✅ LLM 止损已更新: %.2f → %.2f (%s)",
		pos.Symbol, oldStop, newStopLoss, reason))

	// Persist to database
	// 持久化到数据库
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err == nil && posRecord != nil {
			posRecord.CurrentStopLoss = newStopLoss
			posRecord.StopLossOrderID = pos.StopLossOrderID // ✅ 同步止损单 ID
			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新数据库止损失败: %v", err))
			} else {
				sm.logger.Info(fmt.Sprintf("✓ 数据库已同步新止损价: %.2f", newStopLoss))
			}
		}
	}

	return nil
}

// UpdatePositionPriceFromKlines updates position with REAL highest/lowest price from Klines
// UpdatePositionPriceFromKlines 使用 K 线数据更新持仓的真实最高/最低价
//
// This method queries Binance Klines API to get the REAL highest/lowest price
// since position entry, avoiding the issue of missing price movements between
// system execution intervals (every 15 minutes).
// 此方法查询币安 K 线 API 获取开仓以来的真实最高/最低价，
// 避免错过系统执行间隔（每 15 分钟）之间的价格波动。
//
// Example: If system runs at 10:00 (entry $900), 10:15 (price $920),
// but price spiked to $930 at 10:07, traditional sampling would miss $930.
// Klines API guarantees we capture the real $930 highest price.
// 示例：系统在 10:00（开仓 $900）、10:15（价格 $920）运行，
// 但价格在 10:07 飙升到 $930，传统采样会错过 $930。
// K 线 API 保证我们捕获到真实的 $930 最高价。
func (sm *StopLossManager) UpdatePositionPriceFromKlines(ctx context.Context, symbol string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return nil // 无持仓 / No position
	}
	sm.mu.Unlock()

	binanceSymbol := normalizedSymbol

	// Query Klines from entry time to now
	// 查询从开仓时间到现在的所有 K 线
	klines, err := sm.executor.client.NewKlinesService().
		Symbol(binanceSymbol).
		Interval("15m"). // 使用 15 分钟 K 线（与系统运行间隔一致）
		StartTime(pos.EntryTime.UnixMilli()).
		EndTime(time.Now().UnixMilli()).
		Do(ctx)

	if err != nil {
		return fmt.Errorf("获取 K 线数据失败: %w", err)
	}

	if len(klines) == 0 {
		return fmt.Errorf("未获取到 K 线数据")
	}

	// Find REAL highest/lowest price from all klines
	// 从所有 K 线中找出真实的最高/最低价
	var realHighest, realLowest float64
	for i, k := range klines {
		high, _ := parseFloat(k.High)
		low, _ := parseFloat(k.Low)

		if i == 0 {
			realHighest = high
			realLowest = low
		} else {
			if high > realHighest {
				realHighest = high
			}
			if low < realLowest {
				realLowest = low
			}
		}
	}

	// Get current price from latest kline
	// 从最新 K 线获取当前价格
	currentPrice, _ := parseFloat(klines[len(klines)-1].Close)

	// Calculate unrealized PnL
	// 计算未实现盈亏
	var unrealizedPnL float64
	if pos.Side == "long" {
		unrealizedPnL = (currentPrice - pos.EntryPrice) * pos.Quantity
	} else {
		unrealizedPnL = (pos.EntryPrice - currentPrice) * pos.Quantity
	}

	// Update memory
	// 更新内存
	if pos.Side == "long" {
		pos.HighestPrice = realHighest
	} else {
		pos.HighestPrice = realLowest // 空仓用 HighestPrice 字段存储最低价
	}
	pos.CurrentPrice = currentPrice
	pos.UnrealizedPnL = unrealizedPnL

	// Update database immediately
	// 立即更新数据库
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err == nil && posRecord != nil {
			posRecord.HighestPrice = pos.HighestPrice
			posRecord.CurrentPrice = pos.CurrentPrice
			posRecord.UnrealizedPnL = pos.UnrealizedPnL

			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新 %s 数据库失败: %v", symbol, err))
			}
		}
	}

	// Log update
	// 记录更新
	priceType := "最高价"
	if pos.Side == "short" {
		priceType = "最低价"
	}
	sm.logger.Info(fmt.Sprintf("【%s】价格已更新: 当前=%.2f, %s=%.2f (基于 %d 根K线)",
		pos.Symbol, currentPrice, priceType, pos.HighestPrice, len(klines)))

	return nil
}

// ReconcilePosition reconciles in-memory position with actual Binance position
// ReconcilePosition 对账内存持仓与币安实际持仓
//
// This method detects if a stop-loss order has been triggered by comparing
// the position in memory with the actual position on Binance. If the position
// exists in memory but not on Binance, it means the stop-loss was triggered
// and the position needs to be cleaned up.
// 此方法通过对比内存中的持仓与币安实际持仓，检测止损单是否已触发。
// 如果内存中有持仓但币安没有，说明止损单已触发，需要清理持仓数据。
//
// This is critical for server-side stop-loss strategy where Binance executes
// the stop-loss automatically, and the system needs to sync this change.
// 这对于服务器端止损策略至关重要，因为币安会自动执行止损，系统需要同步这个变化。
func (sm *StopLossManager) ReconcilePosition(ctx context.Context, symbol string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	managedPos, exists := sm.positions[normalizedSymbol]
	sm.mu.Unlock()

	if !exists {
		return nil // No position in memory, nothing to reconcile
	}

	// Get actual position from Binance
	// 从币安获取实际持仓
	actualPos, err := sm.executor.GetCurrentPosition(ctx, symbol)
	if err != nil {
		sm.logger.Warning(fmt.Sprintf("⚠️  对账失败（无法获取 %s 币安持仓）: %v", symbol, err))
		return err
	}

	// Case 1: Position exists in memory but NOT on Binance → Stop-loss triggered
	// 情况1：内存有持仓但币安没有 → 止损单已触发
	if actualPos == nil {
		sm.logger.Warning(fmt.Sprintf("🔔【%s】检测到止损单已触发（币安无持仓，内存有持仓）", symbol))
		sm.logger.Info(fmt.Sprintf("   持仓详情: %s %.4f @ $%.2f, 止损价: $%.2f",
			managedPos.Side, managedPos.Quantity, managedPos.EntryPrice, managedPos.CurrentStopLoss))

		// Get current market price as close price
		// 获取当前市场价格作为平仓价格
		closePrice, err := sm.getCurrentPrice(ctx, symbol)
		if err != nil || closePrice == 0 {
			sm.logger.Warning(fmt.Sprintf("⚠️  无法获取平仓价格，使用止损价: %.2f", managedPos.CurrentStopLoss))
			closePrice = managedPos.CurrentStopLoss
		}

		// Calculate realized PnL
		// 计算已实现盈亏
		var realizedPnL float64
		if managedPos.Side == "long" {
			realizedPnL = (closePrice - managedPos.EntryPrice) * managedPos.Quantity
		} else {
			realizedPnL = (managedPos.EntryPrice - closePrice) * managedPos.Quantity
		}

		// Close position (removes from memory and updates database)
		// 关闭持仓（从内存移除并更新数据库）
		reason := "止损单触发（币安自动执行）"
		if err := sm.ClosePosition(ctx, symbol, closePrice, reason, realizedPnL); err != nil {
			sm.logger.Warning(fmt.Sprintf("⚠️  清理已止损持仓失败: %v", err))
			return err
		}

		sm.logger.Success(fmt.Sprintf("✅【%s】已清理止损后的持仓数据（盈亏: %+.2f USDT）", symbol, realizedPnL))
		return nil
	}

	// Case 2: Position exists on both sides → Validate consistency
	// 情况2：币安和内存都有持仓 → 验证一致性

	// Check position side
	// 检查持仓方向
	if actualPos.Side != managedPos.Side {
		sm.logger.Warning(fmt.Sprintf("⚠️【%s】持仓方向不一致！币安:%s, 内存:%s，以币安为准",
			symbol, actualPos.Side, managedPos.Side))
		managedPos.Side = actualPos.Side
	}

	// Check position size (with 0.1% tolerance for rounding)
	// 检查持仓数量（允许0.1%的舍入误差）
	tolerance := managedPos.Quantity * 0.001
	sizeDiff := math.Abs(actualPos.Size - managedPos.Quantity)
	if sizeDiff > tolerance && sizeDiff > 0.001 {
		sm.logger.Warning(fmt.Sprintf("⚠️【%s】持仓数量不一致！币安:%.4f, 内存:%.4f，以币安为准",
			symbol, actualPos.Size, managedPos.Quantity))
		managedPos.Quantity = actualPos.Size
		managedPos.Size = actualPos.Size
	}

	return nil
}

// CheckStopLossOrderStatus checks if stop-loss order still exists on Binance
// CheckStopLossOrderStatus 检查止损单是否仍在币安存在
//
// This method queries the status of the stop-loss order on Binance. If the order
// is filled or no longer exists, it triggers position reconciliation.
// 此方法查询币安上止损单的状态。如果订单已成交或不再存在，则触发持仓对账。
//
// This is an auxiliary method that provides more precise close price information
// when a stop-loss is triggered.
// 这是一个辅助方法，当止损触发时能提供更精确的平仓价格信息。
func (sm *StopLossManager) CheckStopLossOrderStatus(ctx context.Context, symbol string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.RLock()
	pos, exists := sm.positions[normalizedSymbol]
	sm.mu.RUnlock()

	if !exists || pos.StopLossOrderID == "" {
		return nil // No position or no stop-loss order
	}

	binanceSymbol := normalizedSymbol

	// Query order status from Binance
	// 从币安查询订单状态
	order, err := sm.executor.client.NewGetOrderService().
		Symbol(binanceSymbol).
		OrderID(parseInt64(pos.StopLossOrderID)).
		Do(ctx)

	if err != nil {
		// If order not found, it may have been executed
		// 如果订单不存在，可能已被执行
		if strings.Contains(err.Error(), "Unknown order") || strings.Contains(err.Error(), "Order does not exist") {
			sm.logger.Warning(fmt.Sprintf("🔔【%s】止损单已不存在（可能已执行），订单ID: %s", symbol, pos.StopLossOrderID))
			// Trigger reconciliation to clean up
			// 触发对账以清理持仓
			return sm.ReconcilePosition(ctx, symbol)
		}
		return fmt.Errorf("查询止损单状态失败: %w", err)
	}

	// Check if order is filled
	// 检查订单是否已成交
	if order.Status == futures.OrderStatusTypeFilled {
		sm.logger.Warning(fmt.Sprintf("🔔【%s】止损单已成交，订单ID: %s, 状态: %s",
			symbol, pos.StopLossOrderID, order.Status))

		// Get executed price from order
		// 从订单获取成交价格
		closePrice, err := parseFloat(order.AvgPrice)
		if err != nil || closePrice == 0 {
			sm.logger.Warning(fmt.Sprintf("⚠️  无法解析成交价格，使用止损价: %.2f", pos.CurrentStopLoss))
			closePrice = pos.CurrentStopLoss
		}

		// Calculate realized PnL
		// 计算已实现盈亏
		var realizedPnL float64
		if pos.Side == "long" {
			realizedPnL = (closePrice - pos.EntryPrice) * pos.Quantity
		} else {
			realizedPnL = (pos.EntryPrice - closePrice) * pos.Quantity
		}

		// Close position
		// 关闭持仓
		reason := fmt.Sprintf("止损单成交（订单ID: %s）", pos.StopLossOrderID)
		return sm.ClosePosition(ctx, symbol, closePrice, reason, realizedPnL)
	}

	// Order still active
	// 订单仍活跃
	sm.logger.Info(fmt.Sprintf("✓【%s】止损单状态正常: %s", symbol, order.Status))
	return nil
}

// UpdatePosition updates position price and checks if stop-loss should trigger
// UpdatePosition 更新持仓价格并检查是否应触发止损
//
// DEPRECATED: This method is part of the deprecated local monitoring system.
// 已弃用：此方法是已弃用的本地监控系统的一部分。
// Use Binance server-side STOP_MARKET orders instead.
// 请使用币安服务器端 STOP_MARKET 订单。
func (sm *StopLossManager) UpdatePosition(ctx context.Context, symbol string, currentPrice float64) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return nil // 无持仓 / No position
	}
	sm.mu.Unlock()

	// Update price
	// 更新价格
	pos.UpdatePrice(currentPrice)

	// Check if stop-loss should be triggered (simple fixed stop-loss check)
	// 检查是否应该触发止损（简单的固定止损检查）
	if pos.ShouldTriggerStopLoss() {
		sm.logger.Warning(fmt.Sprintf("【%s】触发止损！当前价: %.2f, 止损价: %.2f",
			pos.Symbol, pos.CurrentPrice, pos.CurrentStopLoss))
		return sm.executeStopLoss(ctx, pos)
	}

	return nil
}

// placeStopLossOrder places a stop-loss order on Binance
// placeStopLossOrder 在币安下止损单
func (sm *StopLossManager) placeStopLossOrder(ctx context.Context, pos *Position, stopPrice float64) error {
	// Get current market price for validation
	// 获取当前市场价格用于验证
	currentPrice, err := sm.getCurrentPrice(ctx, pos.Symbol)
	if err != nil {
		return fmt.Errorf("获取当前价格失败: %w", err)
	}

	// Validate stop-loss price to prevent immediate trigger
	// 验证止损价格以防止立即触发
	if pos.Side == "short" {
		// 空仓止损买入：止损价格必须高于当前市场价
		if stopPrice <= currentPrice {
			sm.logger.Warning(fmt.Sprintf("【%s】❌ 空仓止损价格设置错误: %.2f <= 当前价 %.2f (会立即触发)",
				pos.Symbol, stopPrice, currentPrice))
			return fmt.Errorf("空仓止损价格 %.2f 必须高于当前市场价 %.2f，否则会立即触发", stopPrice, currentPrice)
		}
	} else {
		// 多仓止损卖出：止损价格必须低于当前市场价
		if stopPrice >= currentPrice {
			sm.logger.Warning(fmt.Sprintf("【%s】❌ 多仓止损价格设置错误: %.2f >= 当前价 %.2f (会立即触发)",
				pos.Symbol, stopPrice, currentPrice))
			return fmt.Errorf("多仓止损价格 %.2f 必须低于当前市场价 %.2f，否则会立即触发", stopPrice, currentPrice)
		}
	}

	var orderSide futures.SideType
	if pos.Side == "short" {
		orderSide = futures.SideTypeBuy
	} else {
		orderSide = futures.SideTypeSell
	}

	binanceSymbol := sm.config.GetBinanceSymbolFor(pos.Symbol)

	// Create stop-loss order
	// 创建止损单
	order, err := sm.executor.client.NewCreateOrderService().
		Symbol(binanceSymbol).
		Side(orderSide).
		Type(futures.OrderTypeStopMarket).
		StopPrice(fmt.Sprintf("%.2f", stopPrice)).
		Quantity(fmt.Sprintf("%.4f", pos.Quantity)).
		ReduceOnly(true). // 只平仓不开仓 / Close only
		Do(ctx)

	if err != nil {
		return fmt.Errorf("下止损单失败: %w", err)
	}

	pos.StopLossOrderID = fmt.Sprintf("%d", order.OrderID)
	sm.logger.Success(fmt.Sprintf("【%s】止损单已下达: %.2f (订单ID: %s, 当前价: %.2f)",
		pos.Symbol, stopPrice, pos.StopLossOrderID, currentPrice))

	return nil
}

// cancelStopLossOrder cancels an existing stop-loss order
// cancelStopLossOrder 取消现有的止损单
func (sm *StopLossManager) cancelStopLossOrder(ctx context.Context, pos *Position) error {
	if pos.StopLossOrderID == "" {
		return nil
	}

	// Normalize symbol to Binance format
	// 统一符号格式为币安格式
	binanceSymbol := sm.config.GetBinanceSymbolFor(pos.Symbol)

	// Log cancellation attempt
	// 记录取消尝试
	sm.logger.Info(fmt.Sprintf("【%s】正在取消止损单: OrderID=%s, Symbol=%s",
		pos.Symbol, pos.StopLossOrderID, binanceSymbol))

	_, err := sm.executor.client.NewCancelOrderService().
		Symbol(binanceSymbol).
		OrderID(parseInt64(pos.StopLossOrderID)).
		Do(ctx)

	if err != nil {
		// Provide detailed error context
		// 提供详细的错误上下文
		return fmt.Errorf("取消止损单失败 (Symbol=%s, OrderID=%s): %w",
			binanceSymbol, pos.StopLossOrderID, err)
	}

	sm.logger.Success(fmt.Sprintf("【%s】旧止损单已取消: %s", pos.Symbol, pos.StopLossOrderID))
	pos.StopLossOrderID = ""

	return nil
}

// executeStopLoss executes stop-loss (close position)
// executeStopLoss 执行止损（平仓）
//
// DEPRECATED: This method is part of the deprecated local monitoring system.
// 已弃用：此方法是已弃用的本地监控系统的一部分。
// Binance STOP_MARKET orders handle stop-loss execution automatically.
// 币安 STOP_MARKET 订单会自动处理止损执行。
func (sm *StopLossManager) executeStopLoss(ctx context.Context, pos *Position) error {
	sm.logger.Warning(fmt.Sprintf("【%s】🛑 执行止损平仓", pos.Symbol))

	// Close position via market order
	// 通过市价单平仓
	action := ActionCloseLong
	if pos.Side == "short" {
		action = ActionCloseShort
	}

	result := sm.executor.ExecuteTrade(ctx, pos.Symbol, action, pos.Quantity, "触发止损")

	if result.Success {
		sm.logger.Success(fmt.Sprintf("【%s】止损平仓成功，盈亏: %.2f%%",
			pos.Symbol, pos.GetUnrealizedPnL()*100))
		sm.RemovePosition(pos.Symbol)
	} else {
		sm.logger.Error(fmt.Sprintf("【%s】止损平仓失败: %s", pos.Symbol, result.Message))
		return fmt.Errorf("止损平仓失败: %s", result.Message)
	}

	return nil
}

// MonitorPositions monitors all positions in real-time (every 10 seconds)
// MonitorPositions 实时监控所有持仓（每 10 秒）
//
// DEPRECATED: This method is deprecated and should NOT be used with fixed stop-loss strategy.
// 已弃用：此方法已弃用，不应与固定止损策略一起使用。
//
// Reason: With Binance server-side STOP_MARKET orders, local monitoring is redundant and can cause issues:
// 原因：使用币安服务器端 STOP_MARKET 订单时，本地监控是多余的，可能导致问题：
//  1. Duplicate execution: Both Binance and local monitoring may try to close the position
//     重复执行：币安和本地监控可能都尝试平仓
//  2. API overhead: Polling price every 10 seconds for all positions
//     API 开销：每 10 秒为所有持仓轮询价格
//  3. Slower than Binance: 10s polling vs millisecond server-side trigger
//     比币安慢：10 秒轮询 vs 毫秒级服务器端触发
//  4. Reliability: Depends on local program uptime and network stability
//     可靠性：依赖本地程序运行和网络稳定性
//
// For fixed stop-loss strategy, rely entirely on Binance STOP_MARKET orders placed via PlaceInitialStopLoss().
// 对于固定止损策略，完全依赖通过 PlaceInitialStopLoss() 下达的币安 STOP_MARKET 订单。
func (sm *StopLossManager) MonitorPositions(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sm.logger.Info(fmt.Sprintf("🔍 启动持仓监控，间隔: %v", interval))

	for {
		select {
		case <-sm.ctx.Done():
			sm.logger.Info("持仓监控已停止")
			return

		case <-ticker.C:
			sm.mu.RLock()
			positions := make([]*Position, 0, len(sm.positions))
			for _, pos := range sm.positions {
				positions = append(positions, pos)
			}
			sm.mu.RUnlock()

			for _, pos := range positions {
				// Get latest price from Binance
				// 从币安获取最新价格
				currentPrice, err := sm.getCurrentPrice(sm.ctx, pos.Symbol)
				if err != nil {
					sm.logger.Warning(fmt.Sprintf("获取 %s 价格失败: %v", pos.Symbol, err))
					continue
				}

				// Update position and check stop-loss trigger
				// 更新持仓并检查止损触发
				if err := sm.UpdatePosition(sm.ctx, pos.Symbol, currentPrice); err != nil {
					sm.logger.Error(fmt.Sprintf("更新 %s 持仓失败: %v", pos.Symbol, err))
				}
			}
		}
	}
}

// getCurrentPrice gets current price from Binance
// getCurrentPrice 从币安获取当前价格
func (sm *StopLossManager) getCurrentPrice(ctx context.Context, symbol string) (float64, error) {
	binanceSymbol := sm.config.GetBinanceSymbolFor(symbol)

	prices, err := sm.executor.client.NewListPricesService().
		Symbol(binanceSymbol).
		Do(ctx)

	if err != nil {
		return 0, fmt.Errorf("获取价格失败: %w", err)
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("未获取到价格数据")
	}

	price, err := parseFloat(prices[0].Price)
	if err != nil {
		return 0, fmt.Errorf("解析价格失败: %w", err)
	}

	return price, nil
}

// GetAllPositions returns all active positions
// GetAllPositions 返回所有活跃持仓
func (sm *StopLossManager) GetAllPositions() []*Position {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	positions := make([]*Position, 0, len(sm.positions))
	for _, pos := range sm.positions {
		positions = append(positions, pos)
	}
	return positions
}

// Stop stops the stop-loss manager
// Stop 停止止损管理器
func (sm *StopLossManager) Stop() {
	sm.cancel()
}

// Helper function to parse int64
// 辅助函数：解析 int64
func parseInt64(s string) int64 {
	var i int64
	fmt.Sscanf(s, "%d", &i)
	return i
}
