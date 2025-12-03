package managers

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/dataflows"
	"github.com/oak/crypto-trading-bot/internal/executors"
	"github.com/oak/crypto-trading-bot/internal/logger"
	"github.com/oak/crypto-trading-bot/internal/scheduler"
)

// TrailingStopManager 独立的追踪止损管理器（每3分钟运行一次）
// TrailingStopManager Independent trailing stop manager (runs every 3 minutes)
//
// 职责 Responsibilities:
//  1. 每3分钟获取3m K线数据，更新持仓最高/最低价
//     Fetch 3m Klines every 3 minutes and update position highest/lowest prices
//  2. 从4h时间周期获取ATR(7)用于追踪止损计算（缓存15分钟）
//     Fetch ATR(7) from 4h timeframe for trailing stop calculation (cached for 15 minutes)
//  3. 调用追踪止损计算器计算新止损价
//     Call trailing stop calculator to compute new stop price
//  4. 更新币安止损单并同步数据库
//     Update Binance stop-loss orders and sync database
//  5. 对账持仓状态，检测止损触发
//     Reconcile positions and detect stop-loss triggers
//
// 设计原则 Design Principles:
//   - 完全独立于主循环运行（解耦）
//     Runs completely independent from main loop (decoupled)
//   - 复用 StopLossManager 的线程安全方法
//     Reuses thread-safe methods from StopLossManager
//   - ATR缓存策略减少90% API调用
//     ATR caching strategy reduces 90% API calls
type TrailingStopManager struct {
	stopLossManager *executors.StopLossManager // 复用现有止损管理器 / Reuse existing stop-loss manager
	marketData      *dataflows.MarketData      // 市场数据获取 / Market data fetcher
	config          *config.Config             // 配置 / Config
	logger          *logger.ColorLogger        // 日志 / Logger

	// ATR缓存（避免重复API调用）/ ATR cache (avoid duplicate API calls)
	atrCache     map[string]float64 // symbol -> ATR(7)
	atrCacheMu   sync.RWMutex       // ATR缓存锁 / ATR cache lock
	atrCacheTime time.Time          // ATR缓存时间 / ATR cache timestamp
	atrCacheTTL  time.Duration      // ATR缓存有效期（默认15分钟）/ ATR cache TTL (default 15 minutes)

	scheduler *scheduler.TradingScheduler // 3分钟调度器 / 3-minute scheduler

	ctx    context.Context
	cancel context.CancelFunc
}

// NewTrailingStopManager 创建独立追踪止损管理器
// NewTrailingStopManager creates a new independent trailing stop manager
func NewTrailingStopManager(
	cfg *config.Config,
	stopLossManager *executors.StopLossManager,
	log *logger.ColorLogger,
) *TrailingStopManager {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建3分钟调度器 / Create 3-minute scheduler
	scheduler3m, err := scheduler.NewTradingScheduler("3m")
	if err != nil {
		log.Error(fmt.Sprintf("创建3分钟调度器失败: %v", err))
		cancel()
		return nil
	}

	// 创建独立的MarketData实例 / Create independent MarketData instance
	marketData := dataflows.NewMarketData(cfg)

	return &TrailingStopManager{
		stopLossManager: stopLossManager,
		marketData:      marketData,
		config:          cfg,
		logger:          log,
		atrCache:        make(map[string]float64),
		atrCacheTTL:     15 * time.Minute, // ATR缓存15分钟有效 / ATR cache valid for 15 minutes
		scheduler:       scheduler3m,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start 启动独立追踪止损管理器（独立goroutine）
// Start starts the independent trailing stop manager (separate goroutine)
func (tsm *TrailingStopManager) Start() {
	tsm.logger.Info("独立追踪止损管理器已启动，每3分钟执行一次（对齐K线边界）")

	// 每分钟检查一次是否到达3分钟边界
	// Check every minute if we've reached a 3-minute boundary
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-tsm.ctx.Done():
			tsm.logger.Info("独立追踪止损管理器已停止")
			return

		case <-ticker.C:
			// 检查是否到达3分钟K线边界
			// Check if we've reached a 3-minute Kline boundary
			if tsm.scheduler.IsOnTimeframe() {
				tsm.logger.Subheader("执行追踪止损管理（3分钟周期）", '─', 80)
				startTime := time.Now()

				// 执行追踪止损更新
				// Execute trailing stop update
				tsm.updateAllPositions(tsm.ctx)

				elapsed := time.Since(startTime)
				tsm.logger.Success(fmt.Sprintf("追踪止损管理完成，耗时: %.2f秒", elapsed.Seconds()))
			}
		}
	}
}

// Stop 停止独立追踪止损管理器
// Stop stops the independent trailing stop manager
func (tsm *TrailingStopManager) Stop() {
	tsm.logger.Info("正在停止独立追踪止损管理器...")
	tsm.cancel()
}

// updateAllPositions 更新所有活跃持仓的追踪止损
// updateAllPositions updates trailing stops for all active positions
func (tsm *TrailingStopManager) updateAllPositions(ctx context.Context) {
	// 获取所有活跃持仓
	// Get all active positions
	positions := tsm.stopLossManager.GetAllPositions()
	if len(positions) == 0 {
		tsm.logger.Info("当前无活跃持仓，跳过追踪止损更新")
		return
	}

	tsm.logger.Info(fmt.Sprintf("检测到 %d 个活跃持仓，开始并行更新", len(positions)))

	// 使用WaitGroup并行更新所有持仓
	// Use WaitGroup to update all positions in parallel
	var wg sync.WaitGroup
	for _, pos := range positions {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()
			if err := tsm.updateSinglePosition(ctx, symbol); err != nil {
				tsm.logger.Error(fmt.Sprintf("【%s】更新失败: %v", symbol, err))
			}
		}(pos.Symbol)
	}

	wg.Wait()
}

// updateSinglePosition 更新单个持仓的追踪止损（核心逻辑）
// updateSinglePosition updates trailing stop for a single position (core logic)
func (tsm *TrailingStopManager) updateSinglePosition(ctx context.Context, symbol string) error {
	tsm.logger.Info(fmt.Sprintf("【%s】开始更新追踪止损...", symbol))

	// 1. 使用3m K线更新持仓最高价/最低价
	// 1. Update position highest/lowest price using 3m Klines
	if err := tsm.updatePositionPriceFrom3mKline(ctx, symbol); err != nil {
		return fmt.Errorf("更新持仓价格失败: %w", err)
	}

	// 2. 对账持仓（检测止损单是否触发）
	// 2. Reconcile position (check if stop-loss was triggered)
	if err := tsm.stopLossManager.ReconcilePosition(ctx, symbol); err != nil {
		tsm.logger.Warning(fmt.Sprintf("【%s】对账持仓失败: %v", symbol, err))
		// 继续执行，不中断流程 / Continue execution, don't interrupt
	}

	// 3. 检查止损单状态
	// 3. Check stop-loss order status
	if err := tsm.stopLossManager.CheckStopLossOrderStatus(ctx, symbol); err != nil {
		tsm.logger.Warning(fmt.Sprintf("【%s】检查止损单状态失败: %v", symbol, err))
		// 继续执行，不中断流程 / Continue execution, don't interrupt
	}

	// 4. 获取4h级别的ATR(7)（优先使用缓存）
	// 4. Get ATR(7) from 4h timeframe (prefer cache)
	atr, err := tsm.getATRWithCache(ctx, symbol)
	if err != nil {
		return fmt.Errorf("获取ATR失败: %w", err)
	}

	// 5. 调用追踪止损计算和更新
	// 5. Call trailing stop calculation and update
	if err := tsm.stopLossManager.AutoUpdateTrailingStop(ctx, symbol, atr); err != nil {
		return fmt.Errorf("自动更新追踪止损失败: %w", err)
	}

	return nil
}

// updatePositionPriceFrom3mKline 使用3m K线更新持仓最高/最低价
// updatePositionPriceFrom3mKline updates position highest/lowest price using 3m Klines
func (tsm *TrailingStopManager) updatePositionPriceFrom3mKline(ctx context.Context, symbol string) error {
	normalizedSymbol := tsm.config.GetBinanceSymbolFor(symbol)

	// 获取最新的3m K线（limit=1）
	// Fetch the latest 3m Kline (limit=1)
	binanceSymbol := normalizedSymbol
	klines, err := tsm.marketData.GetOHLCV(ctx, binanceSymbol, "3m", 1)
	if err != nil {
		return fmt.Errorf("获取3m K线失败: %w", err)
	}

	if len(klines) == 0 {
		return fmt.Errorf("未获取到3m K线数据")
	}

	latestKline := klines[len(klines)-1]
	klineHigh := latestKline.High
	klineLow := latestKline.Low

	// 获取持仓信息
	// Get position info
	positions := tsm.stopLossManager.GetAllPositions()
	var currentPos *executors.Position
	for _, pos := range positions {
		if pos.Symbol == normalizedSymbol {
			currentPos = pos
			break
		}
	}

	if currentPos == nil {
		return nil // 持仓已不存在（可能已平仓） / Position no longer exists (may have been closed)
	}

	// 增量更新最高/最低价
	// Incrementally update highest/lowest price
	var newHighestPrice float64
	var priceUpdated bool

	if currentPos.Side == "long" {
		// 多仓：比较K线最高价与当前最高价
		// Long position: compare Kline high with current highest price
		if klineHigh > currentPos.HighestPrice {
			newHighestPrice = klineHigh
			priceUpdated = true
		} else {
			newHighestPrice = currentPos.HighestPrice
		}
	} else {
		// 空仓：比较K线最低价与当前最低价
		// Short position: compare Kline low with current lowest price
		if klineLow < currentPos.HighestPrice || currentPos.HighestPrice == currentPos.EntryPrice {
			newHighestPrice = klineLow
			priceUpdated = true
		} else {
			newHighestPrice = currentPos.HighestPrice
		}
	}

	// 如果价格有更新，同步到StopLossManager
	// If price was updated, sync to StopLossManager
	if priceUpdated {
		currentPrice := latestKline.Close
		if err := tsm.stopLossManager.UpdatePositionHighestPrice(symbol, newHighestPrice, currentPrice); err != nil {
			return fmt.Errorf("更新持仓价格失败: %w", err)
		}

		priceType := "最低价"
		if currentPos.Side == "long" {
			priceType = "最高价"
		}
		tsm.logger.Success(fmt.Sprintf("【%s】✅ %s已更新: %.2f → %.2f (3m K线)",
			symbol, priceType, currentPos.HighestPrice, newHighestPrice))
	}

	return nil
}

// getATRWithCache 获取4h级别的ATR(7)（优先使用缓存）
// getATRWithCache gets ATR(7) from 4h timeframe (prefer cache)
func (tsm *TrailingStopManager) getATRWithCache(ctx context.Context, symbol string) (float64, error) {
	normalizedSymbol := tsm.config.GetBinanceSymbolFor(symbol)

	// 检查缓存是否有效
	// Check if cache is valid
	tsm.atrCacheMu.RLock()
	cachedATR, exists := tsm.atrCache[normalizedSymbol]
	cacheAge := time.Since(tsm.atrCacheTime)
	tsm.atrCacheMu.RUnlock()

	if exists && cacheAge < tsm.atrCacheTTL {
		// 缓存命中且有效
		// Cache hit and valid
		tsm.logger.Info(fmt.Sprintf("【%s】使用缓存的ATR(7): %.2f (缓存年龄: %.1f分钟)",
			symbol, cachedATR, cacheAge.Minutes()))
		return cachedATR, nil
	}

	// 缓存失效或不存在，从币安获取
	// Cache invalid or doesn't exist, fetch from Binance
	tsm.logger.Info(fmt.Sprintf("【%s】ATR缓存失效或不存在，从币安获取4h K线...", symbol))
	atr, err := tsm.fetchATRFromBinance(ctx, normalizedSymbol)
	if err != nil {
		// 如果获取失败但有旧缓存，使用旧缓存
		// If fetch fails but old cache exists, use old cache
		if exists {
			tsm.logger.Warning(fmt.Sprintf("【%s】获取新ATR失败，使用旧缓存: %.2f", symbol, cachedATR))
			return cachedATR, nil
		}
		return 0, fmt.Errorf("获取ATR失败且无缓存: %w", err)
	}

	// 更新缓存
	// Update cache
	tsm.atrCacheMu.Lock()
	tsm.atrCache[normalizedSymbol] = atr
	tsm.atrCacheTime = time.Now()
	tsm.atrCacheMu.Unlock()

	tsm.logger.Success(fmt.Sprintf("【%s】✅ ATR(7)已缓存: %.2f (有效期: %d分钟)",
		symbol, atr, int(tsm.atrCacheTTL.Minutes())))

	return atr, nil
}

// fetchATRFromBinance 从币安获取4h K线并计算ATR(7)
// fetchATRFromBinance fetches 4h Klines from Binance and calculates ATR(7)
func (tsm *TrailingStopManager) fetchATRFromBinance(ctx context.Context, symbol string) (float64, error) {
	// 获取4h K线数据（足够计算ATR(7)的数据量）
	// Fetch 4h Klines (enough data to calculate ATR(7))
	longerTimeframe := tsm.config.CryptoLongerTimeframe
	if longerTimeframe == "" {
		longerTimeframe = "4h" // 默认4h / Default 4h
	}

	// 获取足够的历史数据（ATR需要至少14根K线，我们获取20根以确保足够）
	// Fetch enough historical data (ATR needs at least 14 Klines, we fetch 20 to ensure enough)
	lookbackDays := 5 // 5天足够获取20根4h K线 / 5 days is enough for 20 4h Klines
	ohlcvData, err := tsm.marketData.GetOHLCV(ctx, symbol, longerTimeframe, lookbackDays)
	if err != nil {
		return 0, fmt.Errorf("获取%s K线失败: %w", longerTimeframe, err)
	}

	if len(ohlcvData) < 14 {
		return 0, fmt.Errorf("K线数据不足（需要至少14根，实际: %d）", len(ohlcvData))
	}

	// 计算技术指标（包含ATR）
	// Calculate technical indicators (including ATR)
	atrPeriod := tsm.config.TrailingStopATRPeriod
	if atrPeriod == 0 {
		atrPeriod = 7 // 默认7 / Default 7
	}
	indicators := dataflows.CalculateIndicators(ohlcvData, atrPeriod)

	// 提取ATR(7)的最新值
	// Extract the latest ATR(7) value
	lastIdx := len(ohlcvData) - 1
	var atr float64
	if len(indicators.ATR_7) > lastIdx && !math.IsNaN(indicators.ATR_7[lastIdx]) {
		atr = indicators.ATR_7[lastIdx]
	} else {
		return 0, fmt.Errorf("ATR(7)计算失败或为NaN")
	}

	if atr <= 0 {
		return 0, fmt.Errorf("ATR(7)值无效: %.4f", atr)
	}

	return atr, nil
}
