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
// Responsibilities:
// 职责：
//  1. Position lifecycle management (register, remove, query)
//     持仓生命周期管理（注册、移除、查询）
//  2. Binance stop-loss order placement and cancellation
//     币安止损单下单和取消
//  3. Position data storage and retrieval
//     持仓数据存储和检索
//  4. Automatic trailing stop calculation (local, deterministic)
//     自动追踪止损计算（本地、确定性）
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
	positions  map[string]*Position    // symbol -> Position
	executor   *BinanceExecutor        // 执行器 / Executor
	config     *config.Config          // 配置 / Config
	logger     *logger.ColorLogger     // 日志 / Logger
	storage    *storage.Storage        // 数据库 / Database
	calculator *TrailingStopCalculator // 追踪止损计算器 / Trailing stop calculator
	mu         sync.RWMutex            // 读写锁 / RW mutex
	ctx        context.Context         // 上下文 / Context
	cancel     context.CancelFunc      // 取消函数 / Cancel function
}

// NewStopLossManager creates a new StopLossManager
// NewStopLossManager 创建新的止损管理器
func NewStopLossManager(cfg *config.Config, executor *BinanceExecutor, log *logger.ColorLogger, db *storage.Storage) *StopLossManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &StopLossManager{
		positions:  make(map[string]*Position),
		executor:   executor,
		config:     cfg,
		logger:     log,
		storage:    db,
		calculator: NewTrailingStopCalculator(log), // 初始化追踪止损计算器 / Initialize trailing stop calculator
		ctx:        ctx,
		cancel:     cancel,
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

// HasPosition checks if a position exists for the symbol
// HasPosition 检查指定币种是否存在持仓
func (sm *StopLossManager) HasPosition(symbol string) bool {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	_, exists := sm.positions[normalizedSymbol]
	return exists
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

	// Step 3: Update database status with retry
	// 步骤 3：更新数据库状态（带重试）
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

			// Retry database update up to 3 times
			// 重试数据库更新最多 3 次
			for i := 0; i < 3; i++ {
				if err := sm.storage.UpdatePosition(posRecord); err != nil {
					if i == 2 {
						sm.logger.Error(fmt.Sprintf("❌ 更新 %s 数据库状态失败（已重试 3 次）: %v", symbol, err))
						sm.logger.Warning(fmt.Sprintf("⚠️  数据库可能不一致：持仓已从内存删除但数据库状态未更新"))
					} else {
						time.Sleep(time.Millisecond * 100 * time.Duration(i+1))
					}
					continue
				}
				sm.logger.Success(fmt.Sprintf("✅ %s 数据库状态已更新为已关闭", symbol))
				break
			}
		}
	}

	sm.logger.Success(fmt.Sprintf("✅【%s】持仓完全关闭（止损单已取消，内存已清理，数据库已更新）", symbol))
	return nil
}

// PlaceInitialStopLoss places initial stop-loss order for a position
// PlaceInitialStopLoss 为持仓下初始止损单
//
// CRITICAL: This function MUST succeed before the position is considered safe.
// 关键：此函数必须成功才能认为持仓是安全的。
// If this function fails, the caller MUST remove the position from management.
// 如果此函数失败，调用方必须从管理中移除持仓。
func (sm *StopLossManager) PlaceInitialStopLoss(ctx context.Context, pos *Position) error {
	// Validate initial stop-loss distance (relative to entry price)
	// 验证初始止损距离（相对于入场价）
	// This ensures the stop is not too tight (frequent triggers) or too wide (excessive risk)
	// 确保止损不会太紧（频繁触发）或太宽（风险过大）
	if !sm.calculator.ValidateStopDistance(pos.Symbol, pos.EntryPrice, pos.InitialStopLoss, pos.Side) {
		sm.logger.Error(fmt.Sprintf("❌【%s】初始止损距离不合理: 入场价=%.2f, 止损价=%.2f, 方向=%s",
			pos.Symbol, pos.EntryPrice, pos.InitialStopLoss, pos.Side))
		return fmt.Errorf("初始止损距离超出合理范围，拒绝开仓")
	}

	// Try to place stop-loss order
	// 尝试下止损单
	err := sm.placeStopLossOrder(ctx, pos, pos.InitialStopLoss)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("❌ 下初始止损单失败: %v", err))
		sm.logger.Warning(fmt.Sprintf("⚠️  持仓 %s 已注册但无止损保护，建议立即移除或手动下单", pos.Symbol))
		return fmt.Errorf("下初始止损单失败，持仓无保护: %w", err)
	}

	// Sync stop-loss order ID to database
	// 同步止损单 ID 到数据库
	if sm.storage != nil && pos.StopLossOrderID != "" {
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err == nil && posRecord != nil {
			posRecord.StopLossOrderID = pos.StopLossOrderID
			// Retry database update up to 3 times
			// 重试数据库更新最多 3 次
			for i := 0; i < 3; i++ {
				if err := sm.storage.UpdatePosition(posRecord); err != nil {
					if i == 2 {
						sm.logger.Warning(fmt.Sprintf("⚠️  更新数据库止损单 ID 失败（已重试 3 次）: %v", err))
						// Don't fail the function, stop-loss order is already placed
						// 不使函数失败，止损单已经下达
					}
					time.Sleep(time.Millisecond * 100 * time.Duration(i+1))
					continue
				}
				sm.logger.Info(fmt.Sprintf("✓ 数据库已同步止损单 ID: %s", pos.StopLossOrderID))
				break
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

// validateStopLossPrice validates if a stop-loss price is valid for the given position
// validateStopLossPrice 验证止损价格对于给定持仓是否合法
//
// Returns:
//   - currentPrice: the current market price fetched from Binance
//   - error: nil if validation passes, error with detailed message if fails
//
// 返回值：
//   - currentPrice: 从币安获取的当前市场价
//   - error: 验证通过返回 nil，失败返回详细错误信息
func (sm *StopLossManager) validateStopLossPrice(ctx context.Context, symbol string, pos *Position, newStopLoss float64) (float64, error) {
	// Get current market price for validation
	// 获取当前市场价格用于验证
	currentPrice, err := sm.getCurrentPrice(ctx, symbol)
	if err != nil {
		return 0, fmt.Errorf("获取当前价格失败，无法验证止损价格: %w", err)
	}

	// Validate stop-loss price to prevent immediate trigger
	// 验证止损价格以防止立即触发
	if pos.Side == "short" {
		// 空仓止损买入：止损价格必须高于当前市场价
		if newStopLoss <= currentPrice {
			return currentPrice, fmt.Errorf("空仓止损价格 %.2f 必须高于当前市场价 %.2f", newStopLoss, currentPrice)
		}
	} else {
		// 多仓止损卖出：止损价格必须低于当前市场价
		if newStopLoss >= currentPrice {
			return currentPrice, fmt.Errorf("多仓止损价格 %.2f 必须低于当前市场价 %.2f", newStopLoss, currentPrice)
		}
	}

	// Validation passed
	// 验证通过
	return currentPrice, nil
}

// UpdateStopLoss updates stop-loss price for a position (called by LLM every 15 minutes)
// UpdateStopLoss 更新持仓的止损价格（每 15 分钟由 LLM 调用）
func (sm *StopLossManager) UpdateStopLoss(ctx context.Context, symbol string, newStopLoss float64, reason string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		return fmt.Errorf("持仓 %s 不存在", symbol)
	}

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

	// Check if change is significant enough (threshold from trailing stop calculator config)
	// 检查变化是否足够大（阈值从追踪止损计算器配置读取）
	changePercent := math.Abs((newStopLoss-oldStop)/oldStop) * 100
	threshold := sm.calculator.GetConfig(normalizedSymbol).UpdateThreshold
	if changePercent < threshold {
		sm.logger.Info(fmt.Sprintf("【%s】💡 止损价格变化较小 (%.2f → %.2f, 变化 %.2f%% < 阈值 %.1f%%)，跳过更新以避免频繁调整",
			pos.Symbol, oldStop, newStopLoss, changePercent, threshold))
		return nil
	}

	// Record history
	// 记录历史
	pos.AddStopLossEvent(oldStop, newStopLoss, reason, "llm")

	// CRITICAL FIX: Validate new stop-loss price BEFORE cancelling old order
	// 关键修复：在取消旧订单之前先验证新止损价格
	// This prevents leaving the position unprotected if validation fails
	// 这可以防止验证失败时导致持仓无保护
	currentPrice, err := sm.validateStopLossPrice(ctx, symbol, pos, newStopLoss)
	if err != nil {
		sm.logger.Warning(fmt.Sprintf("【%s】❌ 止损价格验证失败: %v，保留原止损单 %.2f",
			pos.Symbol, err, oldStop))
		return fmt.Errorf("止损价格验证失败，原止损单 %.2f 保持不变: %w", oldStop, err)
	}

	sm.logger.Info(fmt.Sprintf("【%s】✓ 止损价格验证通过: %.2f（当前价: %.2f），开始更新订单",
		pos.Symbol, newStopLoss, currentPrice))

	// Cancel old stop-loss order if exists
	// 取消旧的止损单（如果存在）
	// Now safe to cancel - we've verified the new price is valid
	// 现在可以安全取消 - 我们已验证新价格合法
	if pos.StopLossOrderID != "" {
		if err := sm.cancelStopLossOrder(ctx, pos); err != nil {
			sm.logger.Error(fmt.Sprintf("❌ 取消旧止损单失败: %v", err))
			return fmt.Errorf("无法取消旧止损单（订单ID: %s）: %w", pos.StopLossOrderID, err)
		}
	}

	// Place new stop-loss order
	// 下新的止损单
	if err := sm.placeStopLossOrder(ctx, pos, newStopLoss); err != nil {
		sm.logger.Error(fmt.Sprintf("❌【%s】下新止损单失败: %v，持仓现在无止损保护！", pos.Symbol, err))
		return fmt.Errorf("下止损单失败（旧单已取消）: %w", err)
	}

	pos.CurrentStopLoss = newStopLoss
	modeLabel := ""
	if sm.executor.testMode {
		modeLabel = "🧪 [测试网] "
	}
	sm.logger.Success(fmt.Sprintf("%s【%s】✅ LLM 止损已更新: %.2f → %.2f (%s)",
		modeLabel, pos.Symbol, oldStop, newStopLoss, reason))

	// Persist to database with retry
	// 持久化到数据库（带重试）
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(pos.ID)
		if err == nil && posRecord != nil {
			posRecord.CurrentStopLoss = newStopLoss
			posRecord.StopLossOrderID = pos.StopLossOrderID // ✅ 同步止损单 ID
			// Retry database update up to 3 times
			// 重试数据库更新最多 3 次
			for i := 0; i < 3; i++ {
				if err := sm.storage.UpdatePosition(posRecord); err != nil {
					if i == 2 {
						sm.logger.Warning(fmt.Sprintf("⚠️  更新数据库止损失败（已重试 3 次）: %v", err))
					} else {
						time.Sleep(time.Millisecond * 100 * time.Duration(i+1))
					}
					continue
				}
				sm.logger.Info(fmt.Sprintf("✓ 数据库已同步新止损价: %.2f", newStopLoss))
				break
			}
		}
	}

	return nil
}

// AutoUpdateTrailingStop automatically calculates and updates trailing stop
// AutoUpdateTrailingStop 自动计算并更新追踪止损
//
// This method is called every trading interval (e.g., every 5 minutes) to update
// the trailing stop-loss based on the latest highest/lowest price and ATR.
// 此方法在每个交易间隔（如每 5 分钟）调用，基于最新的最高/最低价和 ATR 更新追踪止损。
//
// It replaces LLM-based stop-loss calculation with deterministic formulas:
// 它使用确定性公式替代基于 LLM 的止损计算：
//   - Long: new_stop = highest_price - 2.0 × ATR(3)
//   - Short: new_stop = lowest_price + 2.0 × ATR(3)
//
// Parameters:
// 参数：
//   - ctx: Context / 上下文
//   - symbol: Trading symbol / 交易对
//   - atr: Current ATR value / 当前 ATR 值
//
// Returns:
// 返回：
//   - error if update fails / 更新失败时返回错误
//   - nil if no position or update not needed / 无持仓或无需更新时返回 nil
func (sm *StopLossManager) AutoUpdateTrailingStop(ctx context.Context, symbol string, atr float64) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.RLock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.RUnlock()
		return nil // No position, nothing to update / 无持仓，无需更新
	}

	// Copy necessary data to avoid holding lock during calculation
	// 复制必要数据，避免在计算期间持有锁
	side := pos.Side
	highestPrice := pos.HighestPrice
	currentStopLoss := pos.CurrentStopLoss
	//entryPrice := pos.EntryPrice
	sm.mu.RUnlock()

	// Validate ATR value
	// 验证 ATR 值
	if atr <= 0 {
		sm.logger.Warning(fmt.Sprintf("【%s】⚠️ ATR 值无效 (%.4f)，跳过追踪止损更新", symbol, atr))
		return nil
	}

	// 1. Calculate new trailing stop price using local formula
	// 1. 使用本地公式计算新的追踪止损价
	newStopLoss := sm.calculator.CalculateTrailingStop(
		symbol,
		highestPrice,
		atr,
		side,
	)

	// 2. Validate stop-loss price is in favorable direction
	// 2. 验证止损价朝有利方向移动
	if !sm.calculator.IsValidUpdate(side, currentStopLoss, newStopLoss) {
		sm.logger.Info(fmt.Sprintf("【%s】💡 止损价未朝有利方向移动 (当前: %.2f → 计算: %.2f)，保持原止损",
			symbol, currentStopLoss, newStopLoss))
		return nil
	}

	// 3. Check if change is significant enough (exceeds threshold)
	// 3. 检查变化是否足够大（超过阈值）
	if !sm.calculator.ShouldUpdate(symbol, currentStopLoss, newStopLoss) {
		changePercent := math.Abs((newStopLoss-currentStopLoss)/currentStopLoss) * 100
		sm.logger.Info(fmt.Sprintf("【%s】💡 止损价变化较小 (%.2f%%)，跳过更新以避免频繁调整",
			symbol, changePercent))
		return nil
	}

	// 4. Call existing UpdateStopLoss method to update Binance stop order
	// 4. 调用现有的 UpdateStopLoss 方法更新币安止损单
	priceType := "最低价"
	if side == "long" {
		priceType = "最高价"
	}
	reason := fmt.Sprintf("追踪止损自动调整（%s=%.2f, ATR=%.2f）",
		priceType, highestPrice, atr)

	err := sm.UpdateStopLoss(ctx, symbol, newStopLoss, reason)
	if err != nil {
		sm.logger.Error(fmt.Sprintf("【%s】❌ 自动更新追踪止损失败: %v", symbol, err))
		return fmt.Errorf("自动更新追踪止损失败: %w", err)
	}

	sm.logger.Success(fmt.Sprintf("【%s】✅ 追踪止损已自动更新: %.2f → %.2f (本地计算)",
		symbol, currentStopLoss, newStopLoss))

	return nil
}

// UpdatePositionPriceFromKlines updates position with REAL highest/lowest price from Klines
// UpdatePositionPriceFromKlines 使用 K 线数据更新持仓的真实最高/最低价
//
// This method queries the LATEST 15-minute Kline from Binance and incrementally updates
// the highest/lowest price by comparing with the stored value in database.
// 此方法从币安获取最新的 15 分钟 K 线，通过与数据库中存储的值比较来增量更新最高/最低价。
//
// Example: System runs every 15 minutes. At 10:15, it fetches the 10:00-10:15 kline.
// If kline.High ($930) > database.highest_price ($920), update to $930.
// Otherwise, keep $920. This avoids re-fetching all historical klines every time.
// 示例：系统每 15 分钟运行一次。在 10:15，获取 10:00-10:15 的 K 线。
// 如果 K 线最高价（$930）> 数据库最高价（$920），更新为 $930。
// 否则保持 $920。这避免了每次都重新获取所有历史 K 线。
func (sm *StopLossManager) UpdatePositionPriceFromKlines(ctx context.Context, symbol string) error {
	// Normalize symbol to match internal storage format
	// 标准化符号以匹配内部存储格式
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	// Step 1: Get position data under lock
	// 步骤 1：在锁内获取持仓数据
	sm.mu.RLock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.RUnlock()
		return nil // 无持仓 / No position
	}
	posID := pos.ID
	posSide := pos.Side
	sm.mu.RUnlock()

	binanceSymbol := normalizedSymbol

	// Get current stored highest_price from database
	// 从数据库获取当前存储的最高/最低价
	var storedHighestPrice float64
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(posID)
		if err == nil && posRecord != nil {
			storedHighestPrice = posRecord.HighestPrice
		} else {
			// Fallback to memory if database read fails
			// 如果数据库读取失败，使用内存中的值
			sm.mu.RLock()
			storedHighestPrice = pos.HighestPrice
			sm.mu.RUnlock()
		}
	} else {
		sm.mu.RLock()
		storedHighestPrice = pos.HighestPrice
		sm.mu.RUnlock()
	}

	// Query ONLY the latest Kline (incremental update)
	// 仅查询最新的 K 线（增量更新）
	// Use configured trading interval instead of hardcoded value
	// 使用配置的交易间隔而不是硬编码值
	klines, err := sm.executor.client.NewKlinesService().
		Symbol(binanceSymbol).
		Interval(sm.config.TradingInterval). // 使用配置的交易间隔（与系统运行间隔一致）
		Limit(1).                            // 只获取最新一根 K 线 / Only fetch the latest kline
		Do(ctx)

	if err != nil {
		return fmt.Errorf("获取 K 线数据失败: %w", err)
	}

	if len(klines) == 0 {
		return fmt.Errorf("未获取到 K 线数据")
	}

	// Parse latest kline data
	// 解析最新 K 线数据
	latestKline := klines[0]
	klineHigh, _ := parseFloat(latestKline.High)
	klineLow, _ := parseFloat(latestKline.Low)
	currentPrice, _ := parseFloat(latestKline.Close)

	// Incrementally update highest/lowest price
	// 增量更新最高/最低价
	var newHighestPrice float64
	var priceUpdated bool

	if posSide == "long" {
		// Long position: compare kline high with stored highest price
		// 多仓：比较 K 线最高价与存储的最高价
		if klineHigh > storedHighestPrice {
			newHighestPrice = klineHigh
			priceUpdated = true
		} else {
			newHighestPrice = storedHighestPrice
			priceUpdated = false
		}
	} else {
		// Short position: compare kline low with stored lowest price (stored in HighestPrice field)
		// 空仓：比较 K 线最低价与存储的最低价（存储在 HighestPrice 字段中）
		if klineLow < storedHighestPrice {
			newHighestPrice = klineLow
			priceUpdated = true
		} else {
			newHighestPrice = storedHighestPrice
			priceUpdated = false
		}
	}

	// Step 2: Calculate PnL and update memory under lock
	// 步骤 2：在锁保护下计算盈亏并更新内存
	sm.mu.Lock()
	// Re-check position still exists
	// 再次检查持仓是否仍存在
	pos, exists = sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return nil // Position was closed during API call / 持仓在 API 调用期间被关闭
	}

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
	pos.HighestPrice = newHighestPrice
	pos.CurrentPrice = currentPrice
	pos.UnrealizedPnL = unrealizedPnL
	sm.mu.Unlock()

	// Update database immediately (outside lock to avoid holding lock during I/O)
	// 立即更新数据库（在锁外执行以避免 I/O 期间持有锁）
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(posID)
		if err == nil && posRecord != nil {
			posRecord.HighestPrice = newHighestPrice
			posRecord.CurrentPrice = currentPrice
			posRecord.UnrealizedPnL = unrealizedPnL

			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				sm.logger.Warning(fmt.Sprintf("⚠️  更新 %s 数据库失败: %v", symbol, err))
			}
		}
	}

	// Log update
	// 记录更新
	priceType := "最高价"
	updateStatus := ""
	if posSide == "short" {
		priceType = "最低价"
	}
	if priceUpdated {
		updateStatus = " ✅ 已更新"
	} else {
		updateStatus = " (无变化)"
	}
	sm.logger.Info(fmt.Sprintf("【%s】价格检查: 当前=%.2f, %s=%.2f%s (K线: %.2f-%.2f)",
		normalizedSymbol, currentPrice, priceType, newHighestPrice, updateStatus, klineLow, klineHigh))

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

	// Step 1: Get position data under lock
	// 步骤 1：在锁内获取持仓数据
	sm.mu.RLock()
	managedPos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.RUnlock()
		return nil // No position in memory, nothing to reconcile
	}
	// Copy necessary data to avoid holding lock during API call
	// 复制必要数据以避免在 API 调用期间持有锁
	posSide := managedPos.Side
	posQuantity := managedPos.Quantity
	posEntryPrice := managedPos.EntryPrice
	posCurrentStopLoss := managedPos.CurrentStopLoss
	sm.mu.RUnlock()

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
			posSide, posQuantity, posEntryPrice, posCurrentStopLoss))

		// Get current market price as close price
		// 获取当前市场价格作为平仓价格
		closePrice, err := sm.getCurrentPrice(ctx, symbol)
		if err != nil || closePrice == 0 {
			sm.logger.Warning(fmt.Sprintf("⚠️  无法获取平仓价格，使用止损价: %.2f", posCurrentStopLoss))
			closePrice = posCurrentStopLoss
		}

		// Calculate realized PnL
		// 计算已实现盈亏
		var realizedPnL float64
		if posSide == "long" {
			realizedPnL = (closePrice - posEntryPrice) * posQuantity
		} else {
			realizedPnL = (posEntryPrice - closePrice) * posQuantity
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

	// Step 2: Update position data under lock if inconsistent
	// 步骤 2：如果不一致，在锁保护下更新持仓数据
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Re-check position still exists
	// 再次检查持仓是否仍存在
	managedPos, exists = sm.positions[normalizedSymbol]
	if !exists {
		return nil // Position was closed during API call
	}

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
		// Check if order not found (likely executed or cancelled)
		// 检查订单是否不存在（可能已执行或已取消）
		// Note: Binance Go SDK doesn't provide typed errors, so we use string matching
		// 注意：币安 Go SDK 不提供类型化错误，所以使用字符串匹配
		// Common error messages: "Unknown order", "Order does not exist", "-2011"
		// 常见错误消息："Unknown order"、"Order does not exist"、"-2011"
		errMsg := err.Error()
		isOrderNotFound := strings.Contains(errMsg, "Unknown order") ||
			strings.Contains(errMsg, "Order does not exist") ||
			strings.Contains(errMsg, "-2011") // Binance error code for unknown order

		if isOrderNotFound {
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
	modeLabel := ""
	if sm.executor.testMode {
		modeLabel = "🧪 [测试网] "
	}
	sm.logger.Success(fmt.Sprintf("%s【%s】止损单已下达: %.2f (订单ID: %s, 当前价: %.2f)",
		modeLabel, pos.Symbol, stopPrice, pos.StopLossOrderID, currentPrice))

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
	modeLabel := ""
	if sm.executor.testMode {
		modeLabel = "🧪 [测试网] "
	}
	sm.logger.Info(fmt.Sprintf("%s【%s】正在取消止损单: OrderID=%s, Symbol=%s",
		modeLabel, pos.Symbol, pos.StopLossOrderID, binanceSymbol))

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

	sm.logger.Success(fmt.Sprintf("%s【%s】旧止损单已取消: %s", modeLabel, pos.Symbol, pos.StopLossOrderID))
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

// UpdatePositionHighestPrice updates the highest/lowest price and current price for a position
// UpdatePositionHighestPrice 更新持仓的最高/最低价和当前价格
// This is used by the independent TrailingStopManager to sync price updates from 3m Klines
// 被独立的TrailingStopManager使用，用于同步3m K线的价格更新
func (sm *StopLossManager) UpdatePositionHighestPrice(symbol string, highestPrice, currentPrice float64) error {
	normalizedSymbol := sm.config.GetBinanceSymbolFor(symbol)

	sm.mu.Lock()
	pos, exists := sm.positions[normalizedSymbol]
	if !exists {
		sm.mu.Unlock()
		return nil // No position / 无持仓
	}

	// Calculate unrealized PnL / 计算未实现盈亏
	var unrealizedPnL float64
	if pos.Side == "long" {
		unrealizedPnL = (currentPrice - pos.EntryPrice) * pos.Quantity
	} else {
		unrealizedPnL = (pos.EntryPrice - currentPrice) * pos.Quantity
	}

	// Update memory / 更新内存
	pos.HighestPrice = highestPrice
	pos.CurrentPrice = currentPrice
	pos.UnrealizedPnL = unrealizedPnL
	posID := pos.ID
	sm.mu.Unlock()

	// Update database / 更新数据库
	if sm.storage != nil {
		posRecord, err := sm.storage.GetPositionByID(posID)
		if err == nil && posRecord != nil {
			posRecord.HighestPrice = highestPrice
			posRecord.CurrentPrice = currentPrice
			posRecord.UnrealizedPnL = unrealizedPnL

			if err := sm.storage.UpdatePosition(posRecord); err != nil {
				return fmt.Errorf("更新数据库失败: %w", err)
			}
		}
	}

	return nil
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
