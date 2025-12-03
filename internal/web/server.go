package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/executors"
	"github.com/oak/crypto-trading-bot/internal/logger"
	"github.com/oak/crypto-trading-bot/internal/portfolio"
	"github.com/oak/crypto-trading-bot/internal/scheduler"
	"github.com/oak/crypto-trading-bot/internal/storage"
)

// Server represents the web monitoring server
// Server 表示 Web 监控服务器
type Server struct {
	config          *config.Config
	logger          *logger.ColorLogger
	storage         *storage.Storage
	stopLossManager *executors.StopLossManager
	scheduler       *scheduler.TradingScheduler
	sessionManager  *SessionManager // Session 管理器 / Session manager
	hertz           *server.Hertz
}

// NewServer creates a new web monitoring server
// NewServer 创建新的 Web 监控服务器
func NewServer(cfg *config.Config, log *logger.ColorLogger, db *storage.Storage, stopLossMgr *executors.StopLossManager, sched *scheduler.TradingScheduler) *Server {
	h := server.Default(server.WithHostPorts(fmt.Sprintf(":%d", cfg.WebPort)))

	s := &Server{
		config:          cfg,
		logger:          log,
		storage:         db,
		stopLossManager: stopLossMgr,
		scheduler:       sched,               // Use provided scheduler / 使用提供的调度器
		sessionManager:  NewSessionManager(), // 初始化 Session 管理器 / Initialize session manager
		hertz:           h,
	}

	s.setupRoutes()

	return s
}

// setupRoutes configures all HTTP routes
// setupRoutes 配置所有 HTTP 路由
func (s *Server) setupRoutes() {
	// Public routes (no authentication required)
	// 公开路由（无需认证）
	s.hertz.GET("/login", s.handleLogin)
	s.hertz.POST("/login", s.handleLogin)
	s.hertz.GET("/health", s.handleHealth)

	// Protected routes (authentication required)
	// 受保护路由（需要认证）
	protected := s.hertz.Group("/", s.AuthMiddleware())
	{
		// Static pages
		// 静态页面
		protected.GET("/", s.handleIndex)
		protected.GET("/sessions", s.handleSessions)
		protected.GET("/session/:id", s.handleSessionDetail)
		protected.GET("/trade-history", s.handleTradeHistory)
		protected.GET("/stats", s.handleStats)
		protected.GET("/logout", s.handleLogout)

		// API endpoints
		// API 端点
		protected.GET("/api/positions", s.handlePositions)
		protected.GET("/api/positions/live", s.handleLivePositions) // ✅ Real-time positions from Binance
		protected.GET("/api/positions/:symbol", s.handlePositionsBySymbol)
		protected.GET("/api/symbols", s.handleSymbols)
		protected.GET("/api/balance/history", s.handleBalanceHistory)
		protected.GET("/api/balance/current", s.handleCurrentBalance)

		// Configuration management
		// 配置管理
		protected.GET("/api/config", s.handleGetConfig)
		protected.POST("/api/config", s.handleUpdateConfig)
		protected.POST("/api/config/save", s.handleSaveConfig)
	}
}

// handleIndex renders the main dashboard
// handleIndex 渲染主仪表板
func (s *Server) handleIndex(ctx context.Context, c *app.RequestContext) {
	// Get stats for the first symbol (or aggregate later)
	// 获取第一个交易对的统计（或稍后聚合）
	var stats map[string]interface{}
	var err error
	if len(s.config.CryptoSymbols) > 0 {
		stats, err = s.storage.GetSessionStats(s.config.CryptoSymbols[0])
		if err != nil {
			c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
			return
		}
	} else {
		stats = map[string]interface{}{
			"total_sessions": 0,
			"executed_count": 0,
			"execution_rate": 0.0,
		}
	}

	sessions, err := s.storage.GetLatestSessions(10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// Get batches (grouped sessions) for batch-based display
	// 获取批次（分组的会话）用于按批次显示
	batches, err := s.storage.GetLatestBatches(10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// Get active positions
	// 获取活跃持仓
	positions, _ := s.storage.GetActivePositions()

	// Create template with custom functions
	// 创建带自定义函数的模板
	funcMap := template.FuncMap{
		"mul": func(a, b float64) float64 {
			return a * b
		},
		"extractAction":           extractActionFromDecision,
		"extractActionWithSymbol": extractActionFromDecisionWithSymbol,
	}
	tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles("internal/web/templates/index.html"))

	data := map[string]interface{}{
		"Symbols":         s.config.CryptoSymbols,
		"KlineTimeframe":  s.config.CryptoTimeframe, // K线数据间隔 / K-line data interval
		"TradingInterval": s.config.TradingInterval, // 系统运行间隔 / System execution interval
		"Stats":           stats,
		"Sessions":        sessions,
		"Batches":         batches, // ✅ Add batches for batch-based display
		"Positions":       positions,
		"CurrentTime":     time.Now().Format("2006-01-02 15:04:05"),
		"NextTradeTime":   s.scheduler.GetNextTimeframeTime().Format("2006-01-02 15:04:05"),
		"LLMEnabled":      s.config.APIKey != "" && s.config.APIKey != "your_openai_key",
		"TestMode":        s.config.BinanceTestMode,
		"AutoExecute":     s.config.AutoExecute,
		"LeverageMin":     s.config.BinanceLeverageMin,
		"LeverageMax":     s.config.BinanceLeverageMax,
		"LeverageDynamic": s.config.BinanceLeverageDynamic,
	}

	// Execute template and render
	// 执行模板并渲染
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}

// handleSessions returns JSON list of sessions
func (s *Server) handleSessions(ctx context.Context, c *app.RequestContext) {
	limit := c.DefaultQuery("limit", "20")
	var limitInt int
	fmt.Sscanf(limit, "%d", &limitInt)

	sessions, err := s.storage.GetLatestSessions(limitInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, utils.H{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// handleSessionDetail returns details of a specific session
// handleSessionDetail 返回特定会话的详细信息
func (s *Server) handleSessionDetail(ctx context.Context, c *app.RequestContext) {
	// Get session ID from URL parameter
	// 从 URL 参数获取会话 ID
	idParam := c.Param("id")
	var sessionID int64
	if _, err := fmt.Sscanf(idParam, "%d", &sessionID); err != nil {
		c.JSON(http.StatusBadRequest, utils.H{"error": "invalid session id"})
		return
	}

	// Get session from database
	// 从数据库获取会话
	session, err := s.storage.GetSessionByID(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, utils.H{"error": err.Error()})
		return
	}

	// Create template with custom functions
	// 创建带自定义函数的模板
	funcMap := template.FuncMap{
		"extractAction":           extractActionFromDecision,
		"extractActionWithSymbol": extractActionFromDecisionWithSymbol,
	}
	tmpl := template.Must(template.New("session_detail.html").Funcs(funcMap).ParseFiles("internal/web/templates/session_detail.html"))

	data := map[string]interface{}{
		"Session": session,
	}

	// Execute template and render
	// 执行模板并渲染
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}

// handleStats returns statistics
// handleStats 返回统计信息
func (s *Server) handleStats(ctx context.Context, c *app.RequestContext) {
	// Get symbol from query parameter, or use first symbol
	// 从查询参数获取交易对，或使用第一个交易对
	symbol := c.DefaultQuery("symbol", "")
	if symbol == "" && len(s.config.CryptoSymbols) > 0 {
		symbol = s.config.CryptoSymbols[0]
	}

	if symbol == "" {
		c.JSON(http.StatusBadRequest, utils.H{"error": "no symbol specified"})
		return
	}

	stats, err := s.storage.GetSessionStats(symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleHealth returns health status
func (s *Server) handleHealth(ctx context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, utils.H{
		"status":  "healthy",
		"time":    time.Now(),
		"version": "1.0.0",
	})
}

// Start starts the web server
func (s *Server) Start() error {
	s.logger.Success(fmt.Sprintf("Web 监控启动: http://localhost:%d", s.config.WebPort))
	s.hertz.Spin()
	return nil
}

// Stop stops the web server
func (s *Server) Stop(ctx context.Context) error {
	return s.hertz.Shutdown(ctx)
}

// handlePositions returns all active positions
// handlePositions 返回所有活跃持仓
func (s *Server) handlePositions(ctx context.Context, c *app.RequestContext) {
	positions, err := s.storage.GetActivePositions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, utils.H{
		"positions": positions,
		"count":     len(positions),
	})
}

// handlePositionsBySymbol returns positions for a specific symbol
// handlePositionsBySymbol 返回特定交易对的持仓
func (s *Server) handlePositionsBySymbol(ctx context.Context, c *app.RequestContext) {
	symbol := c.Param("symbol")
	positions, err := s.storage.GetPositionsBySymbol(symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, utils.H{
		"symbol":    symbol,
		"positions": positions,
		"count":     len(positions),
	})
}

// handleLivePositions returns real-time positions directly from Binance
// handleLivePositions 从币安直接获取实时持仓（不依赖数据库）
func (s *Server) handleLivePositions(ctx context.Context, c *app.RequestContext) {
	// Create executor for querying Binance
	// 创建执行器用于查询币安
	executor := executors.NewBinanceExecutor(s.config, s.logger)

	// Response structure
	// 响应结构
	type PositionResponse struct {
		Symbol           string  `json:"symbol"`
		Side             string  `json:"side"`
		Size             float64 `json:"size"`
		EntryPrice       float64 `json:"entry_price"`
		CurrentPrice     float64 `json:"current_price"`
		UnrealizedPnL    float64 `json:"unrealized_pnl"`
		ROE              float64 `json:"roe"` // Return on Equity percentage
		Leverage         int     `json:"leverage"`
		LiquidationPrice float64 `json:"liquidation_price"`
		CurrentStopLoss  float64 `json:"current_stop_loss"` // Current stop-loss price / 当前止损价格
	}

	var positions []PositionResponse

	// Query all configured symbols
	// 查询所有配置的交易对
	for _, symbol := range s.config.CryptoSymbols {
		pos, err := executor.GetCurrentPosition(ctx, symbol)
		if err != nil {
			s.logger.Warning(fmt.Sprintf("获取 %s 实时持仓失败: %v", symbol, err))
			continue
		}

		// Only include positions with non-zero size
		// 仅包含持仓量不为零的持仓
		if pos != nil && pos.Size > 0 {
			// Calculate ROE (Return on Equity) using official Binance formula
			// 使用币安官方公式计算 ROE（回报率）
			roe := 0.0
			if pos.EntryPrice > 0 && pos.Size > 0 && pos.Leverage > 0 {
				// Initial Margin = (EntryPrice × Quantity) / Leverage
				// 初始保证金 = (开仓价格 × 数量) / 杠杆
				initialMargin := (pos.EntryPrice * pos.Size) / float64(pos.Leverage)
				if initialMargin > 0 {
					// ROE = (UnrealizedPnL / InitialMargin) × 100%
					roe = (pos.UnrealizedPnL / initialMargin) * 100
				}
			}

			// Get current price (use entry price as fallback)
			// 获取当前价格（回退到入场价格）
			currentPrice := pos.EntryPrice
			if pos.CurrentPrice > 0 {
				currentPrice = pos.CurrentPrice
			}

			// Get current stop-loss price from stop-loss manager
			// 从止损管理器获取当前止损价格
			currentStopLoss := 0.0
			if s.stopLossManager != nil {
				managedPos := s.stopLossManager.GetPosition(symbol)
				if managedPos != nil {
					currentStopLoss = managedPos.CurrentStopLoss
				}
			}

			positions = append(positions, PositionResponse{
				Symbol:           symbol,
				Side:             pos.Side,
				Size:             pos.Size,
				EntryPrice:       pos.EntryPrice,
				CurrentPrice:     currentPrice,
				UnrealizedPnL:    pos.UnrealizedPnL,
				ROE:              roe,
				Leverage:         pos.Leverage,
				LiquidationPrice: pos.LiquidationPrice,
				CurrentStopLoss:  currentStopLoss,
			})
		}
	}

	c.JSON(http.StatusOK, utils.H{
		"positions": positions,
		"count":     len(positions),
		"timestamp": time.Now().Format("2006-01-02 15:04:05"),
		"source":    "binance_live", // Indicate this is live data
	})
}

// handleSymbols returns all configured trading symbols
// handleSymbols 返回所有配置的交易对
func (s *Server) handleSymbols(ctx context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, utils.H{
		"symbols":          s.config.CryptoSymbols,
		"count":            len(s.config.CryptoSymbols),
		"kline_timeframe":  s.config.CryptoTimeframe, // K线数据间隔
		"trading_interval": s.config.TradingInterval, // 系统运行间隔
	})
}

// extractActionFromDecision extracts trading action from decision text (legacy version without symbol)
// extractActionFromDecision 从决策文本中提取交易动作（旧版本，不带交易对参数）
func extractActionFromDecision(decision string) string {
	return extractActionFromDecisionWithSymbol(decision, "")
}

// extractActionFromDecisionWithSymbol extracts trading action from decision text for a specific symbol
// extractActionFromDecisionWithSymbol 从决策文本中提取特定交易对的交易动作
func extractActionFromDecisionWithSymbol(decision string, symbol string) string {
	if decision == "" {
		return "UNKNOWN"
	}

	// Convert to uppercase for matching
	// 转换为大写用于匹配
	text := strings.ToUpper(decision)

	// 策略1：检测decision文本是否包含多个交易对的决策块
	// Strategy 1: Detect if decision text contains multiple symbol decision blocks
	// 通过检测是否有多个【符号】标记来判断
	// Detect by counting 【symbol】 markers
	hasMultipleSymbols := strings.Count(text, "【") > 1 ||
		strings.Count(text, "[") > strings.Count(text, "【")+1

	// 策略2：尝试找到该交易对的决策块
	// Strategy 2: Try to find this symbol's decision block
	var symbolBlock string
	var foundSymbol bool

	if symbol != "" {
		symbolMarkers := []string{
			"【" + strings.ToUpper(symbol) + "】",
			"[" + strings.ToUpper(symbol) + "]",
			strings.ToUpper(symbol) + ":",
		}

		for _, marker := range symbolMarkers {
			if idx := strings.Index(text, marker); idx != -1 {
				foundSymbol = true
				// 找到下一个【或结尾
				// Find next 【 or end
				endIdx := strings.Index(text[idx+len(marker):], "【")
				if endIdx == -1 {
					symbolBlock = text[idx:]
				} else {
					symbolBlock = text[idx : idx+len(marker)+endIdx]
				}
				break
			}
		}
	}

	// 策略3：决定搜索范围
	// Strategy 3: Decide search scope
	var searchText string
	if foundSymbol {
		// 找到了该交易对的块，只在块内搜索
		// Found this symbol's block, search only within it
		searchText = symbolBlock
	} else if hasMultipleSymbols && symbol != "" {
		// 文本包含多个交易对但没找到当前交易对
		// Text contains multiple symbols but current symbol not found
		// 说明LLM没有为该交易对生成决策
		// Indicates LLM didn't generate decision for this symbol
		return "NO_DECISION"
	} else {
		// 文本只包含一个决策块（可能是该交易对的专属决策）
		// Text contains only one decision block (possibly dedicated to this symbol)
		// 或者是单币种决策格式（没有【符号】标记）
		// Or single-currency decision format (no 【symbol】 markers)
		searchText = text
	}

	// 策略4：提取动作（按优先级顺序）
	// Strategy 4: Extract action (in priority order)
	patterns := []struct {
		action  string
		matches []string
	}{
		{"BUY", []string{"**交易方向**: BUY", "交易方向: BUY", "ACTION: BUY", "决策: BUY", "建议.*?买入", "建议.*?做多", "开多"}},
		{"SELL", []string{"**交易方向**: SELL", "交易方向: SELL", "ACTION: SELL", "决策: SELL", "建议.*?卖出", "建议.*?做空", "开空"}},
		{"CLOSE_LONG", []string{"**交易方向**: CLOSE_LONG", "交易方向: CLOSE_LONG", "ACTION: CLOSE_LONG", "决策: CLOSE_LONG", "平多", "平掉多单"}},
		{"CLOSE_SHORT", []string{"**交易方向**: CLOSE_SHORT", "交易方向: CLOSE_SHORT", "ACTION: CLOSE_SHORT", "决策: CLOSE_SHORT", "平空", "平掉空单"}},
		{"HOLD", []string{"**交易方向**: HOLD", "交易方向: HOLD", "ACTION: HOLD", "决策: HOLD", "观望", "持有", "不建议操作"}},
	}

	for _, p := range patterns {
		for _, pattern := range p.matches {
			// Try literal match first
			// 先尝试字面匹配
			if strings.Contains(searchText, strings.ToUpper(pattern)) {
				return p.action
			}
			// Try regex match
			// 尝试正则匹配
			if matched, _ := regexp.MatchString(pattern, searchText); matched {
				return p.action
			}
		}
	}

	// 如果找到了该交易对的块但没有提取到动作，返回UNKNOWN
	// If found symbol block but couldn't extract action, return UNKNOWN
	// 否则返回HOLD（兼容旧数据）
	// Otherwise return HOLD (backward compatible)
	if foundSymbol {
		return "UNKNOWN"
	}
	return "HOLD"
}

// handleBalanceHistory returns balance history data as JSON
// handleBalanceHistory 以 JSON 格式返回余额历史数据
func (s *Server) handleBalanceHistory(ctx context.Context, c *app.RequestContext) {
	hours := 24 // Default to last 24 hours / 默认最近 24 小时
	if h := c.Query("hours"); h != "" {
		fmt.Sscanf(h, "%d", &hours)
	}

	history, err := s.storage.GetBalanceHistory(hours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// Format for Chart.js
	// 格式化为 Chart.js 可用的格式
	var timestamps []string
	var totalBalances []float64
	var totalAssets []float64 // 总资产 = 总余额 + 未实现盈亏 / Total Assets = Total Balance + Unrealized PnL
	var availableBalances []float64
	var unrealizedPnLs []float64

	// Determine time format based on data span
	// 根据数据跨度决定时间格式
	var timeFormat string
	if len(history) > 0 {
		firstTime := history[0].Timestamp
		lastTime := history[len(history)-1].Timestamp
		duration := lastTime.Sub(firstTime)

		if duration.Hours() > 24 {
			// More than 24 hours: show date + time
			// 超过24小时：显示日期+时间
			timeFormat = "01-02 15:04"
		} else if duration.Hours() > 1 {
			// 1-24 hours: show time with date if different days
			// 1-24小时：显示时间，跨天则加日期
			if firstTime.Day() != lastTime.Day() {
				timeFormat = "01-02 15:04"
			} else {
				timeFormat = "15:04"
			}
		} else {
			// Less than 1 hour: show hour:minute:second
			// 少于1小时：显示时:分:秒
			timeFormat = "15:04:05"
		}
	} else {
		timeFormat = "15:04"
	}

	for _, h := range history {
		timestamps = append(timestamps, h.Timestamp.Format(timeFormat))
		totalBalances = append(totalBalances, h.TotalBalance)
		totalAsset := h.TotalBalance + h.UnrealizedPnL // 计算总资产 / Calculate total assets
		totalAssets = append(totalAssets, totalAsset)
		availableBalances = append(availableBalances, h.AvailableBalance)
		unrealizedPnLs = append(unrealizedPnLs, h.UnrealizedPnL)
	}

	response := map[string]interface{}{
		"timestamps":        timestamps,
		"total_balance":     totalBalances,
		"total_assets":      totalAssets, // 新增：总资产数据 / New: Total assets data
		"available_balance": availableBalances,
		"unrealized_pnl":    unrealizedPnLs,
	}

	c.JSON(http.StatusOK, response)
}

// handleCurrentBalance returns current real-time balance from Binance
// handleCurrentBalance 返回从币安实时获取的当前余额
func (s *Server) handleCurrentBalance(ctx context.Context, c *app.RequestContext) {
	// Create executor and portfolio manager for real-time balance query
	// 创建执行器和投资组合管理器用于实时余额查询
	executor := executors.NewBinanceExecutor(s.config, s.logger)
	portfolioMgr := portfolio.NewPortfolioManager(s.config, executor, s.logger)

	// Update balance from Binance
	// 从币安更新余额
	if err := portfolioMgr.UpdateBalance(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": fmt.Sprintf("获取余额失败: %v", err)})
		return
	}

	// Update positions for all symbols and sync to database
	// 更新所有交易对的持仓信息并同步到数据库
	for _, symbol := range s.config.CryptoSymbols {
		if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
			s.logger.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
			continue
		}

		// NOTE: Position data is NOT synced to database here.
		// 注意：持仓数据不会在此处同步到数据库。
		// Positions should only be saved when opened (cmd/web/main.go or cmd/main.go).
		// 持仓应该只在开仓时保存（cmd/web/main.go 或 cmd/main.go）。
		// This API endpoint only provides real-time balance and position info to the frontend.
		// 此 API 端点仅向前端提供实时余额和持仓信息。
		// Use /api/positions for database positions or /api/positions/live for Binance live positions.
		// 使用 /api/positions 获取数据库持仓或 /api/positions/live 获取币安实时持仓。
	}

	// Return current balance data
	// 返回当前余额数据
	response := map[string]interface{}{
		"timestamp":         time.Now().Format("2006-01-02 15:04:05"),
		"total_balance":     portfolioMgr.GetTotalBalance(),
		"available_balance": portfolioMgr.GetAvailableBalance(),
		"unrealized_pnl":    portfolioMgr.GetTotalUnrealizedPnL(),
		"positions":         portfolioMgr.GetPositionCount(),
	}

	c.JSON(http.StatusOK, response)
}

// handleTradeHistory renders the full trade history page with pagination
// handleTradeHistory 渲染带分页的完整交易历史页面
func (s *Server) handleTradeHistory(ctx context.Context, c *app.RequestContext) {
	// Get pagination parameters
	// 获取分页参数
	page := 1
	pageSize := 50 // Default page size / 默认每页大小

	if pageStr := c.Query("page"); pageStr != "" {
		fmt.Sscanf(pageStr, "%d", &page)
		if page < 1 {
			page = 1
		}
	}

	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		fmt.Sscanf(pageSizeStr, "%d", &pageSize)
		// Limit page size to valid options
		// 限制每页大小为有效选项
		if pageSize != 20 && pageSize != 50 && pageSize != 100 {
			pageSize = 50
		}
	}

	// Calculate offset
	// 计算偏移量
	offset := (page - 1) * pageSize

	// Get total batch count
	// 获取总批次数
	totalCount, err := s.storage.GetTotalBatchCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// Calculate total pages
	// 计算总页数
	totalPages := (totalCount + pageSize - 1) / pageSize

	// Get batches with pagination
	// 获取分页的批次
	batches, err := s.storage.GetBatchesWithPagination(offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// Create template with custom functions
	// 创建带自定义函数的模板
	funcMap := template.FuncMap{
		"extractAction":           extractActionFromDecision,
		"extractActionWithSymbol": extractActionFromDecisionWithSymbol,
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"iterate": func(count int) []int {
			result := make([]int, count)
			for i := 0; i < count; i++ {
				result[i] = i
			}
			return result
		},
	}
	tmpl := template.Must(template.New("trade_history.html").Funcs(funcMap).ParseFiles("internal/web/templates/trade_history.html"))

	data := map[string]interface{}{
		"Batches":     batches,
		"CurrentPage": page,
		"PageSize":    pageSize,
		"TotalCount":  totalCount,
		"TotalPages":  totalPages,
		"HasPrev":     page > 1,
		"HasNext":     page < totalPages,
	}

	// Execute template and render
	// 执行模板并渲染
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}

// handleGetConfig returns the current trading interval configuration
// handleGetConfig 返回当前的交易间隔配置
func (s *Server) handleGetConfig(ctx context.Context, c *app.RequestContext) {
	response := map[string]interface{}{
		"trading_interval": s.scheduler.GetTimeframe(),
		"available_intervals": []string{
			"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "12h", "1d",
		},
	}
	c.JSON(http.StatusOK, response)
}

// handleUpdateConfig updates the trading interval temporarily (in memory only)
// handleUpdateConfig 临时更新交易间隔（仅在内存中）
func (s *Server) handleUpdateConfig(ctx context.Context, c *app.RequestContext) {
	// Parse request body
	// 解析请求体
	var req struct {
		TradingInterval string `json:"trading_interval"`
	}

	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, utils.H{"error": "Invalid request body"})
		return
	}

	// Validate trading interval
	// 验证交易间隔
	validIntervals := map[string]bool{
		"1m": true, "3m": true, "5m": true, "15m": true, "30m": true,
		"1h": true, "2h": true, "4h": true, "6h": true, "12h": true, "1d": true,
	}

	if !validIntervals[req.TradingInterval] {
		c.JSON(http.StatusBadRequest, utils.H{"error": "Invalid trading interval"})
		return
	}

	// Update scheduler
	// 更新调度器
	if err := s.scheduler.UpdateTimeframe(req.TradingInterval); err != nil {
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	s.logger.Info(fmt.Sprintf("Trading interval updated temporarily (new_interval=%s)", req.TradingInterval))

	c.JSON(http.StatusOK, utils.H{
		"status":           "success",
		"message":          "Trading interval updated temporarily (in memory)",
		"trading_interval": req.TradingInterval,
	})
}

// handleSaveConfig saves the current configuration to .env file
// handleSaveConfig 将当前配置保存到 .env 文件
func (s *Server) handleSaveConfig(ctx context.Context, c *app.RequestContext) {
	// Get current trading interval from scheduler
	// 从调度器获取当前交易间隔
	currentInterval := s.scheduler.GetTimeframe()

	// Prepare updates for .env file
	// 准备 .env 文件的更新
	updates := map[string]string{
		"TRADING_INTERVAL": currentInterval,
	}

	// Save to .env file
	// 保存到 .env 文件
	if err := config.SaveToEnv(".env", updates); err != nil {
		s.logger.Error(fmt.Sprintf("Failed to save config to .env: %v", err))
		c.JSON(http.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	s.logger.Info(fmt.Sprintf("Trading interval saved to .env (trading_interval=%s)", currentInterval))

	c.JSON(http.StatusOK, utils.H{
		"status":           "success",
		"message":          "Configuration saved to .env file",
		"trading_interval": currentInterval,
	})
}
