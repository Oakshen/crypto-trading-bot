package executors

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/jpillora/backoff"
	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/logger"
)

// TradeAction represents trading actions
type TradeAction string

const (
	ActionBuy        TradeAction = "BUY"
	ActionSell       TradeAction = "SELL"
	ActionAddLong    TradeAction = "ADD_LONG"  // 加多仓 / Add to long position
	ActionAddShort   TradeAction = "ADD_SHORT" // 加空仓 / Add to short position
	ActionCloseLong  TradeAction = "CLOSE_LONG"
	ActionCloseShort TradeAction = "CLOSE_SHORT"
	ActionHold       TradeAction = "HOLD"
)

// PositionMode represents the position mode
type PositionMode string

const (
	PositionModeOneWay PositionMode = "oneway"
	PositionModeHedge  PositionMode = "hedge"
)

// MarginType represents the margin type
// MarginType 表示保证金类型
type MarginType string

const (
	MarginTypeCross    MarginType = "cross"    // 全仓模式 / Cross margin
	MarginTypeIsolated MarginType = "isolated" // 逐仓模式 / Isolated margin
)

// Position represents a trading position
type Position struct {
	// Basic position info
	// 基础持仓信息
	ID               string    // 持仓 ID / Position ID
	Symbol           string    // 交易对 / Trading pair
	Side             string    // long/short
	Size             float64   // 持仓大小 / Position size (same as Quantity)
	EntryPrice       float64   // 入场价格 / Entry price
	EntryTime        time.Time // 入场时间 / Entry time
	CurrentPrice     float64   // 当前价格 / Current price
	HighestPrice     float64   // 最高价（多仓）或最低价（空仓）/ Highest/lowest price
	Quantity         float64   // 持仓数量 / Quantity (same as Size)
	UnrealizedPnL    float64   // 未实现盈亏 / Unrealized PnL
	PositionAmt      float64   // 仓位金额 / Position amount
	Leverage         int       // 杠杆倍数 / Leverage
	LiquidationPrice float64   // 强平价格 / Liquidation price

	// Add position support (加仓支持)
	// 加仓支持字段
	FirstEntryPrice   float64 // 首次入场价格 / First entry price (same as EntryPrice for initial position)
	AverageEntryPrice float64 // 平均入场价格 / Weighted average entry price after adding positions
	TotalEntries      int     // 加仓次数（包括首次开仓）/ Total entries including initial open (starts at 1)
	TotalInvestment   float64 // 累计投入本金 (USDT) / Total investment in USDT

	// Stop-loss management
	// 止损管理
	InitialStopLoss   float64 // 初始止损价格 / Initial stop-loss
	CurrentStopLoss   float64 // 当前止损价格 / Current stop-loss
	StopLossType      string  // 止损类型：fixed, breakeven, trailing
	TrailingDistance  float64 // 追踪距离（百分比）/ Trailing distance
	PartialTPExecuted bool    // 是否已执行分批止盈 / Whether partial TP has been executed
	ATR               float64 // ATR 值用于动态追踪距离 / ATR value for dynamic trailing distance

	// Order management
	// 订单管理
	StopLossOrderID string // 当前止损单 ID / Stop-loss order ID

	// History and context
	// 历史和上下文
	StopLossHistory []StopLossEvent // 止损变更历史 / Stop-loss history
	PriceHistory    []PricePoint    // 价格历史 / Price history
	OpenReason      string          // 开仓理由 / Opening reason
	LastLLMReview   time.Time       // 上次 LLM 复查时间 / Last LLM review
	LLMSuggestions  []string        // LLM 建议 / LLM suggestions
}

// StopLossEvent represents a stop-loss change event
// StopLossEvent 表示止损变更事件
type StopLossEvent struct {
	Time    time.Time
	OldStop float64
	NewStop float64
	Reason  string
	Trigger string // program or llm
}

// PricePoint represents a price point in time
// PricePoint 表示价格点
type PricePoint struct {
	Time  time.Time
	Price float64
}

// TradeResult represents the result of a trade execution
type TradeResult struct {
	Success     bool
	Action      TradeAction
	Symbol      string
	Amount      float64
	Timestamp   string
	Reason      string
	TestMode    bool
	OrderID     string
	Price       float64
	Filled      float64
	Message     string
	NewPosition *Position
}

// BinanceExecutor handles Binance futures trading
type BinanceExecutor struct {
	client       *futures.Client
	config       *config.Config
	testMode     bool
	positionMode PositionMode
	logger       *logger.ColorLogger
	tradeHistory []TradeResult
}

// NewBinanceExecutor creates a new BinanceExecutor
// NewBinanceExecutor 创建一个新的 BinanceExecutor
func NewBinanceExecutor(cfg *config.Config, log *logger.ColorLogger) *BinanceExecutor {
	futures.UseTestnet = cfg.BinanceTestMode

	client := futures.NewClient(cfg.BinanceAPIKey, cfg.BinanceAPISecret)

	// Set proxy if configured
	// 如果配置了代理，则设置代理
	if cfg.BinanceProxy != "" {
		proxyURL, err := url.Parse(cfg.BinanceProxy)
		if err != nil {
			log.Warning(fmt.Sprintf("代理 URL 解析失败: %v，将不使用代理", err))
		} else {
			// Create custom HTTP client with proxy
			// 创建带代理的自定义 HTTP 客户端
			httpClient := &http.Client{
				Transport: &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: cfg.BinanceProxyInsecureSkipTLS, // 是否跳过 TLS 验证 / Skip TLS verification
					},
				},
				Timeout: 30 * time.Second,
			}
			client.HTTPClient = httpClient
			// Proxy configured successfully (log removed to reduce verbosity)
			// 代理配置成功（移除日志以减少冗余）
		}
	}

	executor := &BinanceExecutor{
		client:       client,
		config:       cfg,
		testMode:     cfg.BinanceTestMode,
		logger:       log,
		tradeHistory: make([]TradeResult, 0),
	}

	// Mode logging removed from constructor to avoid repetitive logs
	// 从构造函数中移除模式日志以避免重复
	// The mode is logged once during startup in main.go

	return executor
}

// DetectPositionMode detects the current position mode
func (e *BinanceExecutor) DetectPositionMode(ctx context.Context) error {
	if e.positionMode != "" {
		return nil
	}

	// Check user configuration first
	configMode := e.config.BinancePositionMode
	if configMode == "oneway" || configMode == "hedge" {
		e.positionMode = PositionMode(configMode)
		modeName := "单向持仓模式（One-way）"
		if configMode == "hedge" {
			modeName = "双向持仓模式（Hedge）"
		}
		e.logger.Success(fmt.Sprintf("使用配置文件(本地)的持仓模式：%s", modeName))
		//return nil
	}

	// Auto-detect mode
	res, err := e.client.NewGetPositionModeService().Do(ctx)
	if err != nil {
		e.logger.Warning("无法自动检测持仓模式，默认使用单向持仓模式")
		e.positionMode = PositionModeOneWay
		return nil
	}

	if res.DualSidePosition {
		e.positionMode = PositionModeHedge
		e.logger.Success("检测到双向持仓模式（Hedge Mode）")
	} else {
		e.positionMode = PositionModeOneWay
		e.logger.Success("检测到单向持仓模式（One-way Mode）")
	}

	return nil
}

// DetectMarginType detects the current margin type for a symbol
// DetectMarginType 检测指定交易对的当前保证金类型（全仓/逐仓）
func (e *BinanceExecutor) DetectMarginType(ctx context.Context, symbol string) (MarginType, error) {
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)

	var marginType MarginType

	err := e.withRetry(func() error {
		positions, err := e.client.NewGetPositionRiskService().
			Symbol(binanceSymbol).
			Do(ctx)

		if err != nil {
			return err
		}

		// Check margin type from position risk info
		// 从持仓风险信息中获取保证金类型
		if len(positions) > 0 {
			marginTypeStr := strings.ToLower(positions[0].MarginType)
			if marginTypeStr == "cross" {
				marginType = MarginTypeCross
			} else if marginTypeStr == "isolated" {
				marginType = MarginTypeIsolated
			} else {
				// Default to cross if unknown
				// 未知类型默认为全仓
				marginType = MarginTypeCross
			}
		} else {
			// No position data, default to cross
			// 无持仓数据，默认为全仓
			marginType = MarginTypeCross
		}

		return nil
	})

	if err != nil {
		e.logger.Warning("无法检测保证金类型，默认为全仓模式")
		return MarginTypeCross, nil
	}

	return marginType, nil
}

// SetupExchange sets up exchange parameters
func (e *BinanceExecutor) SetupExchange(ctx context.Context, symbol string, leverage int) error {
	// Detect position mode
	if err := e.DetectPositionMode(ctx); err != nil {
		return fmt.Errorf("failed to detect position mode: %w", err)
	}

	// Check current position to avoid leverage reduction error (-4161)
	// 检查当前持仓，避免杠杆降低错误 (-4161)
	currentPosition, err := e.GetCurrentPosition(ctx, symbol)
	if err != nil {
		e.logger.Warning(fmt.Sprintf("⚠️  无法获取当前持仓信息: %v，尝试设置杠杆", err))
	} else if currentPosition != nil {
		// Has position, check if leverage reduction is attempted
		// 有持仓，检查是否尝试降低杠杆
		if leverage < currentPosition.Leverage {
			e.logger.Warning(fmt.Sprintf(
				"⚠️  跳过杠杆设置：有持仓时不允许降低杠杆 (当前 %dx -> 目标 %dx)",
				currentPosition.Leverage, leverage))
			e.logger.Info(fmt.Sprintf("   提示：如需降低杠杆，请先平仓后再设置"))

			// Skip leverage setting but continue with balance check
			// 跳过杠杆设置，但继续检查余额
			goto checkBalance
		} else if leverage == currentPosition.Leverage {
			e.logger.Info(fmt.Sprintf("✓ 杠杆已是 %dx，无需调整", leverage))
			goto checkBalance
		}
		// If leverage > currentPosition.Leverage, continue to set (increase is allowed)
		// 如果 leverage > currentPosition.Leverage，继续设置（允许提高杠杆）
	}

	// Set leverage with retry
	err = e.withRetry(func() error {
		_, err := e.client.NewChangeLeverageService().
			Symbol(e.config.GetBinanceSymbolFor(symbol)).
			Leverage(leverage).
			Do(ctx)
		return err
	})

	if err != nil {
		return fmt.Errorf("failed to set leverage: %w", err)
	}

	e.logger.Success(fmt.Sprintf("设置杠杆倍数: %dx", leverage))

checkBalance:
	// Get balance
	account, err := e.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return fmt.Errorf("failed to get account info: %w", err)
	}

	for _, asset := range account.Assets {
		if asset.Asset == "USDT" {
			balance, _ := parseFloat(asset.AvailableBalance)
			e.logger.Success(fmt.Sprintf("当前 USDT 余额: %.2f", balance))
			break
		}
	}

	return nil
}

// GetCurrentPosition gets the current position for a symbol
func (e *BinanceExecutor) GetCurrentPosition(ctx context.Context, symbol string) (*Position, error) {
	var position *Position

	err := e.withRetry(func() error {
		positions, err := e.client.NewGetPositionRiskService().
			Symbol(e.config.GetBinanceSymbolFor(symbol)).
			Do(ctx)

		if err != nil {
			return err
		}

		for _, pos := range positions {
			posAmt, _ := parseFloat(pos.PositionAmt)
			if posAmt != 0 {
				entryPrice, _ := parseFloat(pos.EntryPrice)
				unrealizedPnL, _ := parseFloat(pos.UnRealizedProfit)
				liquidationPrice, _ := parseFloat(pos.LiquidationPrice)
				leverage, _ := parseInt(pos.Leverage)

				side := "long"
				if posAmt < 0 {
					side = "short"
				}

				position = &Position{
					Side:             side,
					Size:             math.Abs(posAmt),
					EntryPrice:       entryPrice,
					UnrealizedPnL:    unrealizedPnL,
					PositionAmt:      posAmt,
					Symbol:           pos.Symbol,
					Leverage:         leverage,
					LiquidationPrice: liquidationPrice,
				}
				break
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get position: %w", err)
	}

	return position, nil
}

// ExecuteTrade executes a trade
func (e *BinanceExecutor) ExecuteTrade(ctx context.Context, symbol string, action TradeAction, amount float64, reason string) *TradeResult {
	result := &TradeResult{
		Success:   false,
		Action:    action,
		Symbol:    symbol,
		Amount:    amount,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Reason:    reason,
		TestMode:  e.testMode,
	}

	// Get current position
	currentPosition, _ := e.GetCurrentPosition(ctx, symbol)

	// Log trade execution
	e.logger.Header("交易执行", '=', 60)
	e.logger.Info(fmt.Sprintf("动作: %s", action))
	e.logger.Info(fmt.Sprintf("交易对: %s", symbol))
	e.logger.Info(fmt.Sprintf("数量: %.4f", amount))
	e.logger.Info(fmt.Sprintf("理由: %s", reason))
	if currentPosition != nil {
		e.logger.Info(fmt.Sprintf("当前持仓: %s %.4f @ $%.2f",
			currentPosition.Side, currentPosition.Size, currentPosition.EntryPrice))
	} else {
		e.logger.Info("当前持仓: 无")
	}

	if e.testMode {
		e.logger.Warning("测试模式 - 仅模拟交易，不实际下单")

		// In test mode, get current market price for accurate position tracking
		// 测试模式下，获取当前市场价格用于准确的持仓跟踪
		currentPrice, err := e.GetCurrentPrice(ctx, symbol)
		if err != nil {
			e.logger.Warning(fmt.Sprintf("⚠️  测试模式：获取当前价格失败: %v，使用 0.0", err))
			currentPrice = 0.0
		}

		result.Success = true
		result.Price = currentPrice
		result.Filled = amount
		result.Message = fmt.Sprintf("测试模式：模拟交易成功 @ $%.2f", currentPrice)
		return result
	}

	// Detect position mode
	e.DetectPositionMode(ctx)

	// Execute trade based on action
	var err error
	switch action {
	case ActionBuy:
		err = e.executeBuy(ctx, symbol, currentPosition, amount, result)
	case ActionSell:
		err = e.executeSell(ctx, symbol, currentPosition, amount, result)
	case ActionAddLong:
		err = e.executeAddPosition(ctx, symbol, currentPosition, amount, true, result)
	case ActionAddShort:
		err = e.executeAddPosition(ctx, symbol, currentPosition, amount, false, result)
	case ActionCloseLong:
		err = e.executeCloseLong(ctx, symbol, currentPosition, result)
	case ActionCloseShort:
		err = e.executeCloseShort(ctx, symbol, currentPosition, result)
	case ActionHold:
		e.logger.Info("💤 建议观望，不执行交易")
		result.Success = true
		result.Message = "观望，不执行交易"
		return result
	default:
		result.Message = fmt.Sprintf("未知的交易动作: %s", action)
		e.logger.Error(result.Message)
		return result
	}

	if err != nil {
		result.Message = fmt.Sprintf("订单执行失败: %v", err)
		e.logger.Error(result.Message)
		return result
	}

	// Get updated position
	time.Sleep(2 * time.Second)
	newPosition, _ := e.GetCurrentPosition(ctx, symbol)
	result.NewPosition = newPosition

	// Record to history
	e.tradeHistory = append(e.tradeHistory, *result)

	return result
}

func (e *BinanceExecutor) executeBuy(ctx context.Context, symbol string, currentPosition *Position, amount float64, result *TradeResult) error {
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)

	// Close short position if exists
	if currentPosition != nil && currentPosition.Side == "short" {
		e.logger.Info("📤 平空仓...")
		positionSide := futures.PositionSideTypeShort
		if e.positionMode == PositionModeOneWay {
			positionSide = futures.PositionSideTypeBoth
		}

		_, err := e.client.NewCreateOrderService().
			Symbol(binanceSymbol).
			Side(futures.SideTypeBuy).
			PositionSide(positionSide).
			Type(futures.OrderTypeMarket).
			Quantity(fmt.Sprintf("%.4f", currentPosition.Size)).
			Do(ctx)

		if err != nil {
			return err
		}
		time.Sleep(1 * time.Second)
	}

	// Open long position if not already long
	if currentPosition == nil || currentPosition.Side != "long" {
		e.logger.Info("📈 开多仓...")
		positionSide := futures.PositionSideTypeLong
		if e.positionMode == PositionModeOneWay {
			positionSide = futures.PositionSideTypeBoth
		}

		order, err := e.client.NewCreateOrderService().
			Symbol(binanceSymbol).
			Side(futures.SideTypeBuy).
			PositionSide(positionSide).
			Type(futures.OrderTypeMarket).
			Quantity(fmt.Sprintf("%.4f", amount)).
			Do(ctx)

		if err != nil {
			return err
		}

		// Get fill price from order
		// 从订单获取成交价格
		fillPrice, _ := parseFloat(order.AvgPrice)
		if fillPrice == 0 {
			// Fallback: query current market price
			// 回退：查询当前市价
			currentPrice, err := e.GetCurrentPrice(ctx, symbol)
			if err == nil {
				fillPrice = currentPrice
			}
		}

		result.Success = true
		result.OrderID = fmt.Sprintf("%d", order.OrderID)
		result.Price = fillPrice
		result.Message = "订单执行成功"
		e.logger.Success(fmt.Sprintf("✅ 订单执行成功，订单ID: %d, 成交价: %.2f", order.OrderID, fillPrice))
	} else {
		result.Message = "已有多仓，不重复开仓（系统保护：防止意外加仓）"
		e.logger.Warning("⚠️ 已有多仓，不重复开仓")
	}

	return nil
}

func (e *BinanceExecutor) executeSell(ctx context.Context, symbol string, currentPosition *Position, amount float64, result *TradeResult) error {
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)

	// Close long position if exists
	if currentPosition != nil && currentPosition.Side == "long" {
		e.logger.Info("📤 平多仓...")
		positionSide := futures.PositionSideTypeLong
		if e.positionMode == PositionModeOneWay {
			positionSide = futures.PositionSideTypeBoth
		}

		_, err := e.client.NewCreateOrderService().
			Symbol(binanceSymbol).
			Side(futures.SideTypeSell).
			PositionSide(positionSide).
			Type(futures.OrderTypeMarket).
			Quantity(fmt.Sprintf("%.4f", currentPosition.Size)).
			Do(ctx)

		if err != nil {
			return err
		}
		time.Sleep(1 * time.Second)
	}

	// Open short position if not already short
	if currentPosition == nil || currentPosition.Side != "short" {
		e.logger.Info("📉 开空仓...")
		positionSide := futures.PositionSideTypeShort
		if e.positionMode == PositionModeOneWay {
			positionSide = futures.PositionSideTypeBoth
		}

		order, err := e.client.NewCreateOrderService().
			Symbol(binanceSymbol).
			Side(futures.SideTypeSell).
			PositionSide(positionSide).
			Type(futures.OrderTypeMarket).
			Quantity(fmt.Sprintf("%.4f", amount)).
			Do(ctx)

		if err != nil {
			return err
		}

		// Get fill price from order
		// 从订单获取成交价格
		fillPrice, _ := parseFloat(order.AvgPrice)
		if fillPrice == 0 {
			// Fallback: query current market price
			// 回退：查询当前市价
			currentPrice, err := e.GetCurrentPrice(ctx, symbol)
			if err == nil {
				fillPrice = currentPrice
			}
		}

		result.Success = true
		result.OrderID = fmt.Sprintf("%d", order.OrderID)
		result.Price = fillPrice
		result.Message = "订单执行成功"
		e.logger.Success(fmt.Sprintf("✅ 订单执行成功，订单ID: %d, 成交价: %.2f", order.OrderID, fillPrice))
	} else {
		result.Message = "已有空仓，不重复开仓（系统保护：防止意外加仓）"
		e.logger.Warning("⚠️ 已有空仓，不重复开仓")
	}

	return nil
}

// executeAddPosition adds to an existing position (both long and short)
// executeAddPosition 加仓到现有持仓（多仓和空仓）
func (e *BinanceExecutor) executeAddPosition(ctx context.Context, symbol string, currentPosition *Position, amount float64, isLong bool, result *TradeResult) error {
	// Validate current position exists and matches direction
	// 验证当前持仓存在且方向匹配
	if currentPosition == nil {
		result.Message = "没有持仓可加仓"
		e.logger.Warning("⚠️ 没有持仓可加仓")
		return nil
	}

	expectedSide := "long"
	if !isLong {
		expectedSide = "short"
	}

	if currentPosition.Side != expectedSide {
		result.Message = fmt.Sprintf("持仓方向不匹配：当前持仓为 %s，无法加仓到 %s", currentPosition.Side, expectedSide)
		e.logger.Warning(fmt.Sprintf("⚠️ 持仓方向不匹配：当前 %s，尝试加仓 %s", currentPosition.Side, expectedSide))
		return nil
	}

	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)

	// Determine side and position side for the add order
	// 确定加仓订单的方向和持仓侧
	var orderSide futures.SideType
	var positionSide futures.PositionSideType

	if isLong {
		e.logger.Info("📈 加多仓...")
		orderSide = futures.SideTypeBuy
		positionSide = futures.PositionSideTypeLong
	} else {
		e.logger.Info("📉 加空仓...")
		orderSide = futures.SideTypeSell
		positionSide = futures.PositionSideTypeShort
	}

	if e.positionMode == PositionModeOneWay {
		positionSide = futures.PositionSideTypeBoth
	}

	// Place market order to add to position
	// 下市价单加仓
	order, err := e.client.NewCreateOrderService().
		Symbol(binanceSymbol).
		Side(orderSide).
		PositionSide(positionSide).
		Type(futures.OrderTypeMarket).
		Quantity(fmt.Sprintf("%.4f", amount)).
		Do(ctx)

	if err != nil {
		return err
	}

	// Get fill price from order
	// 从订单获取成交价格
	fillPrice, _ := parseFloat(order.AvgPrice)
	if fillPrice == 0 {
		// Fallback: query current market price
		// 回退：查询当前市价
		currentPrice, err := e.GetCurrentPrice(ctx, symbol)
		if err == nil {
			fillPrice = currentPrice
		}
	}

	// Calculate new average entry price
	// 计算新的平均入场价格
	oldQuantity := currentPosition.Quantity
	oldAvgPrice := currentPosition.AverageEntryPrice
	if oldAvgPrice == 0 {
		oldAvgPrice = currentPosition.EntryPrice // Fallback to entry price if average not set
	}

	newQuantity := oldQuantity + amount
	newAvgPrice := (oldAvgPrice*oldQuantity + fillPrice*amount) / newQuantity

	// Calculate investment increase
	// 计算新增投资额
	leverage := float64(currentPosition.Leverage)
	if leverage == 0 {
		leverage = float64(e.config.BinanceLeverage)
	}
	addedInvestment := (fillPrice * amount) / leverage

	// Update position fields
	// 更新持仓字段
	currentPosition.Quantity = newQuantity
	currentPosition.Size = newQuantity
	currentPosition.AverageEntryPrice = newAvgPrice
	currentPosition.TotalEntries++
	currentPosition.TotalInvestment += addedInvestment

	result.Success = true
	result.OrderID = fmt.Sprintf("%d", order.OrderID)
	result.Price = fillPrice
	result.Amount = newQuantity // Return total quantity after adding
	result.Message = fmt.Sprintf("加仓成功：数量 %.4f → %.4f，平均价 %.2f → %.2f",
		oldQuantity, newQuantity, oldAvgPrice, newAvgPrice)

	e.logger.Success(fmt.Sprintf("✅ 加仓成功，订单ID: %d", order.OrderID))
	e.logger.Info(fmt.Sprintf("   原持仓: %.4f @ %.2f", oldQuantity, oldAvgPrice))
	e.logger.Info(fmt.Sprintf("   新增: %.4f @ %.2f", amount, fillPrice))
	e.logger.Info(fmt.Sprintf("   合计: %.4f @ %.2f (平均)", newQuantity, newAvgPrice))
	e.logger.Info(fmt.Sprintf("   累计加仓 %d 次，总投资 %.2f USDT", currentPosition.TotalEntries, currentPosition.TotalInvestment))

	return nil
}

func (e *BinanceExecutor) executeCloseLong(ctx context.Context, symbol string, currentPosition *Position, result *TradeResult) error {
	if currentPosition == nil || currentPosition.Side != "long" {
		result.Message = "没有多仓可平"
		e.logger.Warning("⚠️ 没有多仓可平")
		return nil
	}

	e.logger.Info("📤 平多仓...")
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)
	positionSide := futures.PositionSideTypeLong
	if e.positionMode == PositionModeOneWay {
		positionSide = futures.PositionSideTypeBoth
	}

	// Create order service
	// 创建订单服务
	orderService := e.client.NewCreateOrderService().
		Symbol(binanceSymbol).
		Side(futures.SideTypeSell).
		PositionSide(positionSide).
		Type(futures.OrderTypeMarket).
		Quantity(fmt.Sprintf("%.4f", currentPosition.Size))

	// Only use ReduceOnly in Hedge mode, not in One-way mode
	// 只在双向持仓模式使用 ReduceOnly，单向模式不使用
	if e.positionMode == PositionModeHedge {
		orderService = orderService.ReduceOnly(true)
	}

	order, err := orderService.Do(ctx)

	if err != nil {
		return err
	}

	result.Success = true
	result.OrderID = fmt.Sprintf("%d", order.OrderID)
	result.Message = "订单执行成功"
	e.logger.Success(fmt.Sprintf("✅ 订单执行成功，订单ID: %d", order.OrderID))
	return nil
}

func (e *BinanceExecutor) executeCloseShort(ctx context.Context, symbol string, currentPosition *Position, result *TradeResult) error {
	if currentPosition == nil || currentPosition.Side != "short" {
		result.Message = "没有空仓可平"
		e.logger.Warning("⚠️ 没有空仓可平")
		return nil
	}

	e.logger.Info("📤 平空仓...")
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)
	positionSide := futures.PositionSideTypeShort
	if e.positionMode == PositionModeOneWay {
		positionSide = futures.PositionSideTypeBoth
	}

	// Create order service
	// 创建订单服务
	orderService := e.client.NewCreateOrderService().
		Symbol(binanceSymbol).
		Side(futures.SideTypeBuy).
		PositionSide(positionSide).
		Type(futures.OrderTypeMarket).
		Quantity(fmt.Sprintf("%.4f", currentPosition.Size))

	// Only use ReduceOnly in Hedge mode, not in One-way mode
	// 只在双向持仓模式使用 ReduceOnly，单向模式不使用
	if e.positionMode == PositionModeHedge {
		orderService = orderService.ReduceOnly(true)
	}

	order, err := orderService.Do(ctx)

	if err != nil {
		return err
	}

	result.Success = true
	result.OrderID = fmt.Sprintf("%d", order.OrderID)
	result.Message = "订单执行成功"
	e.logger.Success(fmt.Sprintf("✅ 订单执行成功，订单ID: %d", order.OrderID))
	return nil
}

// GetPositionSummary returns a formatted position summary
// GetPositionSummary 返回格式化的持仓摘要信息
func (e *BinanceExecutor) GetPositionSummary(ctx context.Context, symbol string, stopLossManager *StopLossManager) string {
	var summary strings.Builder

	// Get account balance
	account, err := e.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return fmt.Sprintf("**获取账户信息失败**: %v", err)
	}

	var usdtFree, usdtTotal float64
	for _, asset := range account.Assets {
		if asset.Asset == "USDT" {
			usdtFree, _ = parseFloat(asset.AvailableBalance)
			usdtTotal, _ = parseFloat(asset.WalletBalance)
			break
		}
	}

	// Calculate used margin and usage rate
	// 计算已用保证金和资金使用率
	usedMargin := usdtTotal - usdtFree
	usageRate := 0.0
	if usdtTotal > 0 {
		usageRate = (usedMargin / usdtTotal) * 100
	}

	// Determine risk level based on usage rate
	// 根据资金使用率确定风险等级
	riskLevel := ""
	if usageRate < 30 {
		riskLevel = "✅ 安全"
	} else if usageRate < 50 {
		riskLevel = "⚠️ 谨慎"
	} else if usageRate < 70 {
		riskLevel = "🚨 警戒"
	} else {
		riskLevel = "❌ 危险"
	}

	summary.WriteString("**账户信息**:\n")
	summary.WriteString(fmt.Sprintf("- 总余额: %.2f USDT\n", usdtTotal))
	summary.WriteString(fmt.Sprintf("- 可用余额: %.2f USDT\n", usdtFree))
	summary.WriteString(fmt.Sprintf("- 已用保证金: %.2f USDT\n", usedMargin))
	summary.WriteString(fmt.Sprintf("- 资金使用率: %.1f%% %s\n", usageRate, riskLevel))

	// Get position (prioritize StopLossManager for accurate HighestPrice tracking)
	// 获取持仓（优先从 StopLossManager 获取以获得准确的最高/最低价跟踪）
	var position *Position
	var managedPos *Position // Position from StopLossManager (has HighestPrice)

	if stopLossManager != nil {
		managedPos = stopLossManager.GetPosition(symbol)
	}

	// Always get fresh data from Binance for real-time UnrealizedPnL, LiquidationPrice, etc.
	// 始终从币安获取最新数据（实时盈亏、爆仓价等）
	position, _ = e.GetCurrentPosition(ctx, symbol)

	// If we have both, merge HighestPrice from managed position into fresh position
	// 如果两个都有，将托管持仓的 HighestPrice 合并到最新持仓中
	if position != nil && managedPos != nil {
		position.HighestPrice = managedPos.HighestPrice
		position.CurrentPrice = managedPos.CurrentPrice
		position.InitialStopLoss = managedPos.InitialStopLoss
		position.CurrentStopLoss = managedPos.CurrentStopLoss
	} else if position == nil && managedPos != nil {
		// If Binance API failed, use managed position
		// 如果币安 API 失败，使用托管持仓
		position = managedPos
	}

	if position != nil && position.Side != "" {
		sideCN := "多头"
		if position.Side == "short" {
			sideCN = "空头"
		}

		// Get current price
		ticker, _ := e.client.NewListPriceChangeStatsService().Symbol(e.config.GetBinanceSymbolFor(symbol)).Do(ctx)
		currentPrice := position.EntryPrice
		if len(ticker) > 0 {
			currentPrice, _ = parseFloat(ticker[0].LastPrice)
		}

		// Calculate ROE (Return on Equity) using Binance official formula
		// 使用币安官方公式计算 ROE（回报率）
		// ROE = 未实现盈亏 / 初始保证金
		// ROE = UnrealizedPnL / InitialMargin
		pnlPct := 0.0
		if position.EntryPrice > 0 && position.Size > 0 && position.Leverage > 0 {
			// 初始保证金 = (开仓价格 × 数量) / 杠杆
			// InitialMargin = (EntryPrice × Quantity) / Leverage
			initialMargin := (position.EntryPrice * position.Size) / float64(position.Leverage)
			if initialMargin > 0 {
				// ROE = (未实现盈亏 / 初始保证金) × 100%
				// ROE = (UnrealizedPnL / InitialMargin) × 100%
				pnlPct = (position.UnrealizedPnL / initialMargin) * 100
			}
		}

		summary.WriteString(fmt.Sprintf("**当前持仓 %s**:\n", symbol))
		summary.WriteString(fmt.Sprintf("- 方向: %s (%s)\n", sideCN, strings.ToUpper(position.Side)))
		summary.WriteString(fmt.Sprintf("- 数量: %.4f\n", position.Size))
		summary.WriteString(fmt.Sprintf("- 开仓价格: $%.2f\n", position.EntryPrice))
		summary.WriteString(fmt.Sprintf("- 杠杆倍数: %dx\n", position.Leverage))
		summary.WriteString(fmt.Sprintf("- 当前价格: $%.2f\n", currentPrice))

		// Display highest/lowest price since position entry
		// 显示持仓期间的最高/最低价
		if position.HighestPrice > 0 {
			if position.Side == "long" {
				summary.WriteString(fmt.Sprintf("- 持仓期间最高价: $%.2f", position.HighestPrice))

				// Calculate how far current price is from highest
				// 计算当前价格距离最高价的距离
				priceFromHigh := ((position.HighestPrice - currentPrice) / position.HighestPrice) * 100
				if priceFromHigh > 0.1 {
					summary.WriteString(fmt.Sprintf(" (当前回撤 %.2f%%)\n", priceFromHigh))
				} else {
					summary.WriteString(" (当前在最高点)\n")
				}
			} else {
				summary.WriteString(fmt.Sprintf("- 持仓期间最低价: $%.2f", position.HighestPrice))

				// Calculate how far current price is from lowest
				// 计算当前价格距离最低价的距离
				priceFromLow := ((currentPrice - position.HighestPrice) / position.HighestPrice) * 100
				if priceFromLow > 0.1 {
					summary.WriteString(fmt.Sprintf(" (当前反弹 %.2f%%)\n", priceFromLow))
				} else {
					summary.WriteString(" (当前在最低点)\n")
				}
			}
		}

		summary.WriteString(fmt.Sprintf("- 未实现盈亏: %+.2f USDT (%+.2f%%)\n", position.UnrealizedPnL, pnlPct))

		// Display stop-loss information if available
		// 显示止损信息（如果可用）
		if stopLossManager != nil {
			managedPos := stopLossManager.GetPosition(symbol)
			if managedPos != nil && managedPos.CurrentStopLoss > 0 {
				summary.WriteString(fmt.Sprintf("- 当前止损: $%.2f", managedPos.CurrentStopLoss))

				// Calculate stop-loss distance percentage
				// 计算止损距离百分比
				stopDistance := 0.0
				if position.Side == "long" {
					stopDistance = ((currentPrice - managedPos.CurrentStopLoss) / currentPrice) * 100
				} else {
					stopDistance = ((managedPos.CurrentStopLoss - currentPrice) / currentPrice) * 100
				}
				summary.WriteString(fmt.Sprintf(" (距离当前价 %.2f%%)\n", stopDistance))
			}
		}

		if position.LiquidationPrice > 0 {
			summary.WriteString(fmt.Sprintf("- 爆仓价格: $%.2f\n", position.LiquidationPrice))
		}

	} else {
		summary.WriteString(fmt.Sprintf("**当前持仓 %s**: 无持仓\n", symbol))
	}

	return summary.String()
}

// withRetry executes a function with exponential backoff retry
func (e *BinanceExecutor) withRetry(fn func() error) error {
	b := &backoff.Backoff{
		Min:    2 * time.Second,
		Max:    10 * time.Second,
		Factor: 2,
		Jitter: true,
	}

	maxRetries := 3
	for i := 0; i <= maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}

		if i == maxRetries {
			return fmt.Errorf("max retries reached: %w", err)
		}

		duration := b.Duration()
		e.logger.Warning(fmt.Sprintf("操作失败 (尝试 %d/%d): %v，等待 %.1f 秒后重试...",
			i+1, maxRetries, err, duration.Seconds()))
		time.Sleep(duration)
	}

	return nil
}

// GetAccountInfo gets account information from Binance
// GetAccountInfo 从币安获取账户信息
func (e *BinanceExecutor) GetAccountInfo(ctx context.Context) (*futures.Account, error) {
	return e.client.NewGetAccountService().Do(ctx)
}

// GetBalance returns the available USDT balance
// GetBalance 返回可用的 USDT 余额
func (e *BinanceExecutor) GetBalance(ctx context.Context) (float64, error) {
	account, err := e.GetAccountInfo(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get account info: %w", err)
	}

	// Find USDT balance
	// 查找 USDT 余额
	for _, asset := range account.Assets {
		if asset.Asset == "USDT" {
			balance, err := parseFloat(asset.AvailableBalance)
			if err != nil {
				return 0, fmt.Errorf("failed to parse balance: %w", err)
			}
			return balance, nil
		}
	}

	return 0, fmt.Errorf("USDT balance not found")
}

// GetCurrentPrice returns the current market price for a symbol
// GetCurrentPrice 返回交易对的当前市场价格
func (e *BinanceExecutor) GetCurrentPrice(ctx context.Context, symbol string) (float64, error) {
	binanceSymbol := strings.ReplaceAll(symbol, "/", "")

	// Get latest price from ticker
	// 从行情数据获取最新价格
	prices, err := e.client.NewListPricesService().Symbol(binanceSymbol).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get price: %w", err)
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("no price data for %s", symbol)
	}

	price, err := parseFloat(prices[0].Price)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price: %w", err)
	}

	return price, nil
}

// Helper functions
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// Position helper methods
// Position 辅助方法

// GetUnrealizedPnL calculates unrealized profit/loss percentage
// GetUnrealizedPnL 计算未实现盈亏百分比
func (p *Position) GetUnrealizedPnL() float64 {
	if p.Side == "long" {
		return (p.CurrentPrice - p.EntryPrice) / p.EntryPrice
	}
	// For short positions
	// 空仓
	return (p.EntryPrice - p.CurrentPrice) / p.EntryPrice
}

// GetUnrealizedPnLUSDT calculates unrealized profit/loss in USDT
// GetUnrealizedPnLUSDT 计算 USDT 计价的未实现盈亏
func (p *Position) GetUnrealizedPnLUSDT() float64 {
	return p.GetUnrealizedPnL() * p.EntryPrice * p.Quantity
}

// GetHoldingDuration returns how long the position has been held
// GetHoldingDuration 返回持仓时间
func (p *Position) GetHoldingDuration() time.Duration {
	return time.Since(p.EntryTime)
}

// ShouldTriggerStopLoss checks if stop-loss should be triggered
// ShouldTriggerStopLoss 检查是否应该触发止损
func (p *Position) ShouldTriggerStopLoss() bool {
	if p.Side == "long" {
		return p.CurrentPrice <= p.CurrentStopLoss
	}
	// For short positions
	// 空仓
	return p.CurrentPrice >= p.CurrentStopLoss
}

// GetRiskRewardRatio calculates current risk/reward ratio
// GetRiskRewardRatio 计算当前盈亏比
func (p *Position) GetRiskRewardRatio() float64 {
	risk := p.EntryPrice - p.InitialStopLoss
	if risk <= 0 {
		return 0
	}

	reward := p.CurrentPrice - p.EntryPrice
	if p.Side == "short" {
		reward = p.EntryPrice - p.CurrentPrice
	}

	return reward / risk
}

// UpdatePrice updates current price and highest/lowest price
// UpdatePrice 更新当前价格和最高/最低价
func (p *Position) UpdatePrice(newPrice float64) {
	p.CurrentPrice = newPrice

	// Update highest price for long positions
	// 更新多仓的最高价
	if p.Side == "long" {
		if newPrice > p.HighestPrice {
			p.HighestPrice = newPrice
		}
	} else {
		// Update lowest price for short positions
		// 更新空仓的最低价
		if p.HighestPrice == 0 || newPrice < p.HighestPrice {
			p.HighestPrice = newPrice
		}
	}

	// Add to price history (limit to last 1000 points)
	// 添加到价格历史（限制最近 1000 个点）
	p.PriceHistory = append(p.PriceHistory, PricePoint{
		Time:  time.Now(),
		Price: newPrice,
	})
	if len(p.PriceHistory) > 1000 {
		p.PriceHistory = p.PriceHistory[1:]
	}
}

// AddStopLossEvent adds a stop-loss change event to history
// AddStopLossEvent 添加止损变更事件到历史记录
func (p *Position) AddStopLossEvent(oldStop, newStop float64, reason, trigger string) {
	event := StopLossEvent{
		Time:    time.Now(),
		OldStop: oldStop,
		NewStop: newStop,
		Reason:  reason,
		Trigger: trigger,
	}
	p.StopLossHistory = append(p.StopLossHistory, event)
}

// GetStopLossHistoryString returns formatted stop-loss history
// GetStopLossHistoryString 返回格式化的止损历史字符串
func (p *Position) GetStopLossHistoryString() string {
	if len(p.StopLossHistory) == 0 {
		return "无止损变更历史"
	}

	result := ""
	for i, event := range p.StopLossHistory {
		result += fmt.Sprintf("%d. %s: %.2f → %.2f (%s, 由%s触发)\n",
			i+1,
			event.Time.Format("15:04:05"),
			event.OldStop,
			event.NewStop,
			event.Reason,
			event.Trigger)
	}
	return result
}

// AdjustQuantityPrecision adjusts quantity to match symbol's precision requirements
// AdjustQuantityPrecision 调整数量以符合交易对的精度要求
func AdjustQuantityPrecision(symbol string, quantity float64) (float64, error) {
	// Get precision and min quantity for the symbol
	// 获取交易对的精度和最小数量要求
	precision, minQty := getSymbolPrecision(symbol)

	// Round to the required precision
	// 四舍五入到所需精度
	multiplier := math.Pow(10, float64(precision))
	adjusted := math.Round(quantity*multiplier) / multiplier

	// Ensure it meets minimum quantity
	// 确保满足最小数量要求
	if adjusted < minQty {
		return 0, fmt.Errorf("数量 %.4f 低于最小要求 %.4f (交易对: %s)", adjusted, minQty, symbol)
	}

	return adjusted, nil
}

// getSymbolPrecision returns the quantity precision and minimum quantity for a symbol
// getSymbolPrecision 返回交易对的数量精度和最小数量
func getSymbolPrecision(symbol string) (precision int, minQty float64) {
	// Default values
	// 默认值
	precision = 2
	minQty = 0.01

	// Symbol-specific configurations (based on Binance futures)
	// 特定交易对的配置（基于币安期货）
	switch strings.ToUpper(symbol) {
	case "BTCUSDT", "BTC/USDT":
		precision = 3 // 0.001 BTC
		minQty = 0.001
	case "ETHUSDT", "ETH/USDT":
		precision = 3 // 0.001 ETH
		minQty = 0.001
	case "SOLUSDT", "SOL/USDT":
		precision = 2 // 0.01 SOL (2025-04-02 更新)
		minQty = 0.01
	case "BNBUSDT", "BNB/USDT":
		precision = 2 // 0.01 BNB
		minQty = 0.01
	case "XRPUSDT", "XRP/USDT":
		precision = 1 // 0.1 XRP
		minQty = 0.1
	case "ADAUSDT", "ADA/USDT":
		precision = 0 // 1 ADA
		minQty = 1.0
	case "DOGEUSDT", "DOGE/USDT":
		precision = 0 // 1 DOGE
		minQty = 1.0
	case "DOTUSDT", "DOT/USDT":
		precision = 1 // 0.1 DOT
		minQty = 0.1
	case "MATICUSDT", "MATIC/USDT":
		precision = 0 // 1 MATIC
		minQty = 1.0
	case "AVAXUSDT", "AVAX/USDT":
		precision = 2 // 0.01 AVAX
		minQty = 0.1
	}

	return precision, minQty
}
