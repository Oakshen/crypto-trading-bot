package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	openaiComponent "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/oak/crypto-trading-bot/internal/agents"
	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/constant"
	"github.com/oak/crypto-trading-bot/internal/executors"
	"github.com/oak/crypto-trading-bot/internal/logger"
	"github.com/oak/crypto-trading-bot/internal/managers"
	"github.com/oak/crypto-trading-bot/internal/portfolio"
	"github.com/oak/crypto-trading-bot/internal/scheduler"
	"github.com/oak/crypto-trading-bot/internal/storage"
	"github.com/oak/crypto-trading-bot/internal/web"
)

// Global stop-loss manager
// 全局止损管理器
var globalStopLossManager *executors.StopLossManager

// Global trailing stop manager (independent 3-minute loop)
// 全局追踪止损管理器（独立的3分钟循环）
var globalTrailingStopManager *managers.TrailingStopManager

func main() {
	// Load configuration
	// 加载配置
	cfg, err := config.LoadConfig(constant.BlankStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	// 初始化日志
	logger.Init(cfg.DebugMode)
	log := logger.Global

	log.Header("加密货币交易机器人 - Web 监控模式 (完整版)", '=', 80)
	log.Info(fmt.Sprintf("交易对: %v", cfg.CryptoSymbols))
	log.Info(fmt.Sprintf("时间周期: %s", cfg.CryptoTimeframe))
	log.Info(fmt.Sprintf("回看天数: %d", cfg.CryptoLookbackDays))
	log.Info(fmt.Sprintf("杠杆倍数: %dx", cfg.BinanceLeverage))
	log.Info(fmt.Sprintf("Web 端口: %d", cfg.WebPort))

	if cfg.BinanceTestMode {
		log.Success("🟢 运行模式: 测试模式（模拟交易）")
	} else {
		log.Warning("🔴 运行模式: 实盘模式（真实交易！）")
	}

	// Initialize executor
	// 初始化执行器
	executor := executors.NewBinanceExecutor(cfg, log)

	// Initialize storage
	// 初始化数据库
	log.Subheader("初始化数据库", '─', 80)
	dbDir := filepath.Dir(cfg.DatabasePath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Error(fmt.Sprintf("创建数据库目录失败: %v", err))
		os.Exit(1)
	}

	db, err := storage.NewStorage(cfg.DatabasePath)
	if err != nil {
		log.Error(fmt.Sprintf("初始化数据库失败: %v", err))
		os.Exit(1)
	}
	defer db.Close()

	log.Success(fmt.Sprintf("数据库已连接: %s", cfg.DatabasePath))

	// Display statistics for all symbols
	// 显示所有交易对的统计信息
	for _, symbol := range cfg.CryptoSymbols {
		stats, err := db.GetSessionStats(symbol)
		if err != nil {
			log.Warning(fmt.Sprintf("获取 %s 历史统计失败: %v", symbol, err))
		} else if stats["total_sessions"].(int) > 0 {
			log.Info(fmt.Sprintf("【%s】历史会话: %d, 已执行: %d, 执行率: %.1f%%",
				symbol,
				stats["total_sessions"].(int),
				stats["executed_count"].(int),
				stats["execution_rate"].(float64)))
		}
	}

	ctx := context.Background()

	// Initialize and verify LLM service
	// 初始化并验证 LLM 服务
	log.Subheader("验证 LLM 服务", '─', 80)

	llmCfg := &openaiComponent.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BackendURL,
		Model:   cfg.QuickThinkLLM,
	}

	// Create ChatModel
	chatModel, err := openaiComponent.NewChatModel(ctx, llmCfg)
	if err != nil {
		log.Error(fmt.Sprintf("❌ 创建 LLM 客户端失败: %v", err))
		log.Error("请检查 .env 文件中的 OPENAI_API_KEY 和 OPENAI_BASE_URL 配置")
		os.Exit(1)
	}

	// Test LLM service with a simple call
	// 使用简单调用测试 LLM 服务
	log.Info(fmt.Sprintf("🔍 测试 LLM 服务连接..."))
	log.Info(fmt.Sprintf("   模型: %s", cfg.QuickThinkLLM))
	log.Info(fmt.Sprintf("   API: %s", cfg.BackendURL))

	testMessages := []*schema.Message{
		schema.SystemMessage("你是一个测试助手"),
		schema.UserMessage("请回复：OK"),
	}

	testResponse, err := chatModel.Generate(ctx, testMessages)
	if err != nil {
		log.Error(fmt.Sprintf("❌ LLM 服务测试失败: %v", err))
		log.Error(fmt.Sprintf("请检查配置: API=%s, Model=%s", cfg.BackendURL, cfg.QuickThinkLLM))
		os.Exit(1)
	}

	log.Success("✅ LLM 服务可用")
	if testResponse.ResponseMeta != nil && testResponse.ResponseMeta.Usage != nil {
		log.Info(fmt.Sprintf("   测试消耗 Token: %d", testResponse.ResponseMeta.Usage.TotalTokens))
	}

	// Setup exchange for all symbols
	// 为所有交易对设置交易所参数
	log.Subheader("设置交易所参数", '─', 80)
	for _, symbol := range cfg.CryptoSymbols {
		if err := executor.SetupExchange(ctx, symbol, cfg.BinanceLeverage); err != nil {
			log.Error(fmt.Sprintf("设置 %s 交易所失败: %v", symbol, err))
			os.Exit(1)
		}
		log.Success(fmt.Sprintf("✅ %s 交易所设置完成", symbol))
	}

	// Check margin type and warn if using isolated margin with dynamic leverage
	// 检查保证金类型，如果在逐仓模式下使用动态杠杆则发出警告
	if cfg.BinanceLeverageDynamic && len(cfg.CryptoSymbols) > 0 {
		log.Subheader("保证金模式检查", '─', 80)
		firstSymbol := cfg.CryptoSymbols[0]
		marginType, err := executor.DetectMarginType(ctx, firstSymbol)
		if err != nil {
			log.Warning(fmt.Sprintf("⚠️  无法检测保证金类型: %v", err))
		} else {
			if marginType == "isolated" {
				log.Warning("⚠️  检测到【逐仓模式】+ 动态杠杆配置")
				log.Warning("")
				log.Warning(fmt.Sprintf("   配置: BINANCE_LEVERAGE=%d-%d （动态杠杆）",
					cfg.BinanceLeverageMin, cfg.BinanceLeverageMax))
				log.Warning("   模式: 逐仓模式（Isolated Margin）")
				log.Warning("")
				log.Warning("   ⚠️  重要提示：")
				log.Warning("   • 逐仓模式下，有持仓时不允许降低杠杆（-4161 错误）")
				log.Warning("   • 如果 LLM 动态选择的杠杆低于当前持仓杠杆，将跳过杠杆调整")
				log.Warning("   • 这可能导致实际杠杆与 LLM 选择的杠杆不一致")
				log.Warning("")
				log.Warning("   💡 建议：")
				log.Warning("   1. 切换到全仓模式（Binance 网页 → 合约 → 设置 → 保证金模式 → 全仓）")
				log.Warning("   2. 或使用固定杠杆（例如 BINANCE_LEVERAGE=10）")
				log.Warning("")
			} else {
				log.Success(fmt.Sprintf("✅ 保证金模式: 全仓模式（Cross Margin） - 支持动态杠杆 %d-%d",
					cfg.BinanceLeverageMin, cfg.BinanceLeverageMax))
			}
		}
	}

	// Initialize stop-loss manager
	// 初始化止损管理器
	log.Subheader("初始化止损管理器", '─', 80)
	globalStopLossManager = executors.NewStopLossManager(cfg, executor, log, db)

	// Load existing active positions from database
	// 从数据库加载现有活跃持仓
	activePositions, err := db.GetActivePositions()
	if err != nil {
		log.Warning(fmt.Sprintf("加载活跃持仓失败: %v", err))
	} else if len(activePositions) > 0 {
		log.Info(fmt.Sprintf("发现 %d 个活跃持仓，正在注册到止损管理器...", len(activePositions)))

		// Deduplicate positions by normalized symbol
		// 按标准化符号去重持仓
		// This prevents BTC/USDT and BTCUSDT being treated as separate positions
		// 防止 BTC/USDT 和 BTCUSDT 被当作不同的持仓
		posMap := make(map[string]*storage.PositionRecord)
		for _, posRecord := range activePositions {
			normalizedSymbol := cfg.GetBinanceSymbolFor(posRecord.Symbol)

			// If duplicate found, keep the one with valid entry price
			// 如果发现重复，保留有效入场价的记录
			if existing, ok := posMap[normalizedSymbol]; ok {
				// Prefer record with non-zero entry price
				// 优先选择入场价非零的记录
				if posRecord.EntryPrice > 0 && existing.EntryPrice == 0 {
					log.Warning(fmt.Sprintf("⚠️  发现重复持仓: %s 和 %s，保留入场价非零的记录",
						existing.Symbol, posRecord.Symbol))
					posMap[normalizedSymbol] = posRecord
				} else if posRecord.EntryPrice == 0 && existing.EntryPrice > 0 {
					log.Warning(fmt.Sprintf("⚠️  发现重复持仓: %s 和 %s，保留入场价非零的记录",
						posRecord.Symbol, existing.Symbol))
					// Keep existing
				} else {
					log.Warning(fmt.Sprintf("⚠️  发现重复持仓: %s 和 %s，保留第一个",
						existing.Symbol, posRecord.Symbol))
				}
			} else {
				posMap[normalizedSymbol] = posRecord
			}
		}

		// Register deduplicated positions
		// 注册去重后的持仓
		for normalizedSymbol, posRecord := range posMap {
			// Convert PositionRecord to Position
			// 将 PositionRecord 转换为 Position
			pos := &executors.Position{
				ID:               posRecord.ID,
				Symbol:           normalizedSymbol, // Use normalized symbol / 使用标准化符号
				Side:             posRecord.Side,
				EntryPrice:       posRecord.EntryPrice,
				EntryTime:        posRecord.EntryTime,
				Quantity:         posRecord.Quantity,
				InitialStopLoss:  posRecord.InitialStopLoss,
				CurrentStopLoss:  posRecord.CurrentStopLoss,
				StopLossType:     posRecord.StopLossType,
				TrailingDistance: posRecord.TrailingDistance,
				HighestPrice:     posRecord.HighestPrice,
				CurrentPrice:     posRecord.CurrentPrice,
				OpenReason:       posRecord.OpenReason,
				ATR:              posRecord.ATR,
				StopLossOrderID:  posRecord.StopLossOrderID, // ✅ 恢复止损单 ID
			}
			globalStopLossManager.RegisterPosition(pos)
			log.Success(fmt.Sprintf("已恢复持仓: %s %s @ $%.2f", normalizedSymbol, posRecord.Side, posRecord.EntryPrice))
		}
	} else {
		log.Info("暂无活跃持仓")
	}

	// Initialize and start independent trailing stop manager
	// 初始化并启动独立追踪止损管理器
	log.Subheader("启动独立追踪止损管理器", '─', 80)
	globalTrailingStopManager = managers.NewTrailingStopManager(cfg, globalStopLossManager, log)
	if globalTrailingStopManager != nil {
		go globalTrailingStopManager.Start() // 独立goroutine运行 / Run in separate goroutine
		log.Success("追踪止损管理器已启动（每3分钟执行一次，对齐K线边界）")
	} else {
		log.Warning("追踪止损管理器初始化失败")
	}

	// Initialize portfolio manager for balance tracking
	// 初始化投资组合管理器用于余额跟踪
	portfolioMgr := portfolio.NewPortfolioManager(cfg, executor, log)

	// Save initial balance snapshot
	// 保存初始余额快照
	log.Subheader("保存初始余额快照", '─', 80)
	if err := portfolioMgr.UpdateBalance(ctx); err != nil {
		log.Warning(fmt.Sprintf("⚠️  获取初始余额失败: %v", err))
	} else {
		// Update positions for all symbols
		// 更新所有交易对的持仓信息
		for _, symbol := range cfg.CryptoSymbols {
			if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
				log.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
			}
		}

		initialBalance := &storage.BalanceHistory{
			Timestamp:        time.Now(),
			TotalBalance:     portfolioMgr.GetTotalBalance(),
			AvailableBalance: portfolioMgr.GetAvailableBalance(),
			UnrealizedPnL:    portfolioMgr.GetTotalUnrealizedPnL(),
			Positions:        portfolioMgr.GetPositionCount(),
		}
		if err := db.SaveBalanceHistory(initialBalance); err != nil {
			log.Warning(fmt.Sprintf("⚠️  保存初始余额快照失败: %v", err))
		} else {
			log.Success(fmt.Sprintf("✅ 初始余额快照已保存: 总额=%.2f USDT, 可用=%.2f USDT, 持仓=%d",
				initialBalance.TotalBalance, initialBalance.AvailableBalance, initialBalance.Positions))
		}
	}

	// Note: Local monitoring disabled - relying on Binance server-side stop-loss orders
	// 注意：已禁用本地监控 - 完全依赖币安服务器端止损单
	// 原因：
	//   1. 币安止损单 24/7 服务器端监控，触发速度更快（毫秒级）
	//   2. 避免本地监控与币安止损单重复执行
	//   3. 减少 API 调用开销
	//   4. 即使本地程序崩溃，币安止损单仍会执行
	// go func() {
	// 	log.Success("🔍 启动持仓监控，间隔: 10 秒")
	// 	globalStopLossManager.MonitorPositions(10 * time.Second)
	// }()

	// Start balance history recording in background
	// 在后台启动余额历史记录
	go func() {
		log.Success("📊 启动余额历史记录，间隔: 5 分钟")
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			// Update balance
			if err := portfolioMgr.UpdateBalance(ctx); err != nil {
				log.Warning(fmt.Sprintf("⚠️  更新余额失败: %v", err))
				continue
			}

			// Update positions for all symbols
			for _, symbol := range cfg.CryptoSymbols {
				if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
					log.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
				}
			}

			// Save balance snapshot
			balanceHistory := &storage.BalanceHistory{
				Timestamp:        time.Now(),
				TotalBalance:     portfolioMgr.GetTotalBalance(),
				AvailableBalance: portfolioMgr.GetAvailableBalance(),
				UnrealizedPnL:    portfolioMgr.GetTotalUnrealizedPnL(),
				Positions:        portfolioMgr.GetPositionCount(),
			}
			if err := db.SaveBalanceHistory(balanceHistory); err != nil {
				log.Warning(fmt.Sprintf("⚠️  保存余额历史失败: %v", err))
			} else {
				log.Info(fmt.Sprintf("💾 余额快照已保存: %.2f USDT (持仓: %d)",
					balanceHistory.TotalBalance, balanceHistory.Positions))
			}
		}
	}()

	// Initialize scheduler
	// 初始化调度器（使用 TradingInterval 而不是 CryptoTimeframe）
	// Use TradingInterval instead of CryptoTimeframe for scheduling
	tradingScheduler, err := scheduler.NewTradingScheduler(cfg.TradingInterval)
	if err != nil {
		log.Error(fmt.Sprintf("调度器初始化失败: %v", err))
		os.Exit(1)
	}

	log.Success(fmt.Sprintf("调度器已初始化 (运行间隔: %s, K线间隔: %s)", cfg.TradingInterval, cfg.CryptoTimeframe))

	// Start web server (pass scheduler to enable config updates)
	// 启动 Web 服务器（传递调度器以启用配置更新）
	webServer := web.NewServer(cfg, log, db, globalStopLossManager, tradingScheduler)
	go func() {
		if err := webServer.Start(); err != nil {
			log.Error(fmt.Sprintf("Web 服务器启动失败: %v", err))
		}
	}()

	log.Info(fmt.Sprintf("下一次分析时间: %s", tradingScheduler.GetNextTimeframeTime().Format("2006-01-02 15:04:05")))
	log.Info("")
	log.Info("按 Ctrl+C 停止程序")
	log.Header("开始循环执行", '=', 80)

	// Setup signal handling
	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Trading loop
	// 交易循环
	runCount := 0
	ticker := time.NewTicker(1 * time.Minute) // Check every minute
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			log.Warning("\n收到停止信号，正在关闭...")

			// Stop trailing stop manager
			// 停止追踪止损管理器
			if globalTrailingStopManager != nil {
				globalTrailingStopManager.Stop()
				log.Success("追踪止损管理器已停止")
			}

			// Stop stop-loss manager
			// 停止止损管理器
			globalStopLossManager.Stop()

			// Stop web server
			// 停止Web服务器
			if err := webServer.Stop(ctx); err != nil {
				log.Warning(fmt.Sprintf("Web 服务器停止失败: %v", err))
			}
			return

		case <-ticker.C:
			// Check if it's time to run
			// 检查是否到达执行时间
			if tradingScheduler.IsOnTimeframe() {
				runCount++
				log.Header(fmt.Sprintf("第 %d 次执行", runCount), '=', 80)
				log.Info(fmt.Sprintf("执行时间: %s", time.Now().Format("2006-01-02 15:04:05")))

				// Run trading analysis with auto-execution
				// 运行交易分析并自动执行
				if err := runTradingAnalysis(ctx, cfg, log, executor, db); err != nil {
					log.Error(fmt.Sprintf("交易分析失败: %v", err))
				}

				// Calculate next run time
				// 计算下次执行时间
				nextTime := tradingScheduler.GetNextTimeframeTime()
				log.Info(fmt.Sprintf("下次执行时间: %s", nextTime.Format("2006-01-02 15:04:05")))
				log.Header("等待下一次执行", '=', 80)
			}
		}
	}
}

func runTradingAnalysis(ctx context.Context, cfg *config.Config, log *logger.ColorLogger, executor *executors.BinanceExecutor, db *storage.Storage) error {
	// Create trading graph
	// 创建交易图工作流
	log.Subheader("初始化 Eino Graph 工作流", '─', 80)
	log.Info("创建多智能体分析系统...")
	log.Info("  • 市场分析师 (Market Analyst)")
	log.Info("  • 加密货币分析师 (Crypto Analyst)")
	log.Info("  • 情绪分析师 (Sentiment Analyst)")
	log.Info("  • 交易员 (Trader)")
	log.Info("")

	tradingGraph := agents.NewSimpleTradingGraph(cfg, log, executor, globalStopLossManager)

	// Run the graph workflow
	// 运行工作流
	result, err := tradingGraph.Run(ctx)
	if err != nil {
		return fmt.Errorf("工作流执行失败: %w", err)
	}

	// Display final results
	// 显示最终结果
	log.Subheader("工作流执行结果", '─', 80)

	var decision string
	if d, ok := result["decision"].(string); ok {
		decision = d
		log.Info("最终交易决策:")
		log.Info(decision)
	}

	// Get agent state
	// 获取智能体状态
	state := tradingGraph.GetState()
	log.Subheader("分析师报告摘要", '─', 80)
	for _, symbol := range cfg.CryptoSymbols {
		reports := state.GetSymbolReports(symbol)
		if reports != nil {
			log.Info(fmt.Sprintf("【%s】", symbol))
			log.Info(fmt.Sprintf("  ✅ 市场分析: %d 字符", len(reports.MarketReport)))
			log.Info(fmt.Sprintf("  ✅ 加密货币分析: %d 字符", len(reports.CryptoReport)))
			log.Info(fmt.Sprintf("  ✅ 情绪分析: %d 字符", len(reports.SentimentReport)))
			log.Info(fmt.Sprintf("  ✅ 持仓信息: %d 字符", len(reports.PositionInfo)))
		}
	}

	// Save session to database for each symbol with symbol-specific decision
	// 为每个交易对保存分析结果到数据库，包含该交易对的专属决策
	log.Subheader("保存分析结果", '─', 80)

	// Generate batch ID for this execution (all symbols in this run share the same batch_id)
	// 为本次执行生成批次 ID（本次运行的所有交易对共享相同的 batch_id）
	batchID := fmt.Sprintf("batch-%d", time.Now().Unix())
	log.Info(fmt.Sprintf("批次 ID: %s", batchID))

	// Parse multi-currency decision to extract symbol-specific decisions
	// 解析多币种决策以提取每个交易对的专属决策
	symbolDecisions := agents.ParseMultiCurrencyDecision(decision, cfg.CryptoSymbols)

	for _, symbol := range cfg.CryptoSymbols {
		reports := state.GetSymbolReports(symbol)
		if reports == nil {
			continue
		}

		// Get symbol-specific decision text
		// 获取该交易对的专属决策文本
		symbolDecision := decision // Default to full decision
		if parsedDecision, ok := symbolDecisions[symbol]; ok && parsedDecision.Valid {
			// Format symbol-specific decision for display
			// 格式化该交易对的专属决策用于显示
			symbolDecision = fmt.Sprintf(`【%s】
**交易方向**: %s
**置信度**: %.2f
**杠杆倍数**: %d倍
**理由**: %s`,
				symbol,
				parsedDecision.Action,
				parsedDecision.Confidence,
				parsedDecision.Leverage,
				parsedDecision.Reason)
			// Log successful parsing
			// 记录解析成功
			log.Info(fmt.Sprintf("【%s】决策解析成功: Action=%s, Confidence=%.2f, Leverage=%d",
				symbol, parsedDecision.Action, parsedDecision.Confidence, parsedDecision.Leverage))
		} else {
			// Log parsing failure
			// 记录解析失败
			log.Warning(fmt.Sprintf("【%s】决策解析失败，使用完整决策文本 (可能导致前端显示不准确)", symbol))
		}

		session := &storage.TradingSession{
			BatchID:         batchID, // ✅ Batch ID shared across all symbols in this run
			Symbol:          symbol,
			Timeframe:       cfg.CryptoTimeframe,
			CreatedAt:       time.Now(),
			MarketReport:    reports.MarketReport,
			CryptoReport:    reports.CryptoReport,
			SentimentReport: reports.SentimentReport,
			PositionInfo:    reports.PositionInfo,
			Decision:        symbolDecision, // ✅ Symbol-specific decision
			FullDecision:    decision,       // ✅ Full LLM decision (all symbols)
			Executed:        false,
			ExecutionResult: "",
		}

		sessionID, err := db.SaveSession(session)
		if err != nil {
			log.Warning(fmt.Sprintf("保存 %s 会话失败: %v", symbol, err))
		} else {
			log.Success(fmt.Sprintf("【%s】会话已保存到数据库 (ID: %d)", symbol, sessionID))
		}
	}
	log.Info(fmt.Sprintf("数据库路径: %s", cfg.DatabasePath))

	// Auto-execution logic
	// 自动执行交易逻辑
	if cfg.AutoExecute {
		log.Subheader("自动执行交易", '─', 80)
		log.Info("🚀 自动执行模式已启用")

		// Parse multi-currency decision
		// 解析多币种决策
		decisions := agents.ParseMultiCurrencyDecision(decision, cfg.CryptoSymbols)

		// Initialize portfolio manager
		// 初始化投资组合管理器
		portfolioMgr := portfolio.NewPortfolioManager(cfg, executor, log)
		if err := portfolioMgr.UpdateBalance(ctx); err != nil {
			log.Error(fmt.Sprintf("获取账户余额失败: %v", err))
		}

		// Update positions for all symbols
		// 更新所有交易对的持仓信息
		for _, symbol := range cfg.CryptoSymbols {
			if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
				log.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
			}
		}

		log.Info(portfolioMgr.GetPortfolioSummary())

		// Initialize trade coordinator with stop-loss manager
		// 初始化交易协调器（传入止损管理器）
		coordinator := executors.NewTradeCoordinator(cfg, executor, log, globalStopLossManager)

		// Execute trades for each symbol
		// 为每个交易对执行交易
		executionResults := make(map[string]string)

		for symbol, symbolDecision := range decisions {
			log.Subheader(fmt.Sprintf("处理 %s 交易决策", symbol), '-', 60)

			if !symbolDecision.Valid {
				log.Warning(fmt.Sprintf("⚠️  %s 决策无效: %s", symbol, symbolDecision.Reason))
				executionResults[symbol] = fmt.Sprintf("决策无效: %s", symbolDecision.Reason)
				continue
			}

			log.Info(fmt.Sprintf("交易对: %s", symbol))
			log.Info(fmt.Sprintf("动作: %s", symbolDecision.Action))
			log.Info(fmt.Sprintf("置信度: %.2f", symbolDecision.Confidence))
			log.Info(fmt.Sprintf("理由: %s", symbolDecision.Reason))

			// Handle HOLD actions
			// 处理 HOLD 动作
			if symbolDecision.Action == executors.ActionHold {
				log.Info("💤 观望决策，不执行交易")

				// Update stop-loss if LLM provides new stop-loss price
				// 如果 LLM 提供了新的止损价格，则更新止损
				//if symbolDecision.StopLoss > 0 {
				//	// Check if stop-loss price has changed
				//	// 检查止损价格是否有变化
				//	currentPos := globalStopLossManager.GetPosition(symbol)
				//	if currentPos != nil && currentPos.CurrentStopLoss == symbolDecision.StopLoss {
				//		// Stop-loss price unchanged, skip update
				//		// 止损价格未变化，跳过更新
				//		log.Info(fmt.Sprintf("💡 %s 止损价格未变化 (%.2f)，无需更新", symbol, symbolDecision.StopLoss))
				//		executionResults[symbol] = fmt.Sprintf("观望，止损价格未变化: %.2f", symbolDecision.StopLoss)
				//	} else {
				//		// Stop-loss price changed, execute update
				//		// 止损价格有变化，执行更新
				//		err := globalStopLossManager.UpdateStopLoss(ctx, symbol, symbolDecision.StopLoss, symbolDecision.Reason)
				//		if err != nil {
				//			log.Warning(fmt.Sprintf("⚠️  更新 %s 止损失败: %v", symbol, err))
				//			executionResults[symbol] = fmt.Sprintf("观望，更新止损失败: %v", err)
				//		} else {
				//			oldStop := "无"
				//			if currentPos != nil {
				//				oldStop = fmt.Sprintf("%.2f", currentPos.CurrentStopLoss)
				//			}
				//			log.Success(fmt.Sprintf("✅ %s 止损更新处理完成: %s → %.2f", symbol, oldStop, symbolDecision.StopLoss))
				//			executionResults[symbol] = fmt.Sprintf("观望，止损处理: %s → %.2f", oldStop, symbolDecision.StopLoss)
				//		}
				//	}
				//} else {
				//	executionResults[symbol] = "观望，不执行交易"
				//}
				continue
			}

			// Update position info for this symbol
			// 更新该交易对的持仓信息
			if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
				log.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
			}

			// Get current position
			// 获取当前持仓
			currentPosition, err := executor.GetCurrentPosition(ctx, symbol)
			if err != nil {
				log.Warning(fmt.Sprintf("⚠️  获取 %s 当前持仓失败: %v", symbol, err))
			}

			// Validate decision against current position
			// 验证决策与当前持仓的一致性
			if err := agents.ValidateDecision(symbolDecision, currentPosition); err != nil {
				log.Error(fmt.Sprintf("❌ %s 决策验证失败: %v", symbol, err))
				executionResults[symbol] = fmt.Sprintf("决策验证失败: %v", err)
				continue
			}

			// Execute the trade using coordinator
			// 使用协调器执行交易
			result, err := coordinator.ExecuteDecisionWithParams(
				ctx,
				symbol,
				symbolDecision.Action,
				symbolDecision.Reason,
				symbolDecision.Leverage,
				symbolDecision.PositionSizePercent,
			)
			if err != nil {
				log.Error(fmt.Sprintf("❌ %s 交易执行失败: %v", symbol, err))
				executionResults[symbol] = fmt.Sprintf("执行失败: %v", err)
				continue
			}

			// Display execution summary
			// 显示执行摘要
			log.Info(coordinator.GetExecutionSummary(result))

			if result.Success {
				// Increment trade count for successful execution
				// 交易成功执行，增加交易计数
				tradingGraph.IncrementTradeCount()

				executionResults[symbol] = fmt.Sprintf("✅ 成功执行 %s", result.Action)

				// Handle closing positions: cancel stop-loss and update database
				// 处理平仓：取消止损单并更新数据库
				if symbolDecision.Action == executors.ActionCloseLong || symbolDecision.Action == executors.ActionCloseShort {
					// Get close price and calculate realized PnL
					// 获取平仓价格并计算已实现盈亏
					closePrice := result.Price
					realizedPnL := 0.0
					if currentPosition != nil {
						realizedPnL = currentPosition.UnrealizedPnL
					}

					// Close position completely (cancel stop-loss, remove from memory, update database)
					// 完整关闭持仓（取消止损单、从内存移除、更新数据库）
					closeReason := fmt.Sprintf("LLM决策平仓: %s", symbolDecision.Reason)
					if err := globalStopLossManager.ClosePosition(ctx, symbol, closePrice, closeReason, realizedPnL); err != nil {
						log.Warning(fmt.Sprintf("⚠️  关闭 %s 持仓失败: %v", symbol, err))
					}
				}

				// Register position for stop-loss management (only for opening positions)
				// 注册持仓到止损管理器（仅开仓时）
				if symbolDecision.Action == executors.ActionBuy || symbolDecision.Action == executors.ActionSell {
					// Validate and get leverage to use
					// 验证并获取要使用的杠杆
					leverageToUse := agents.ValidateLeverage(
						symbolDecision.Leverage,
						cfg.BinanceLeverageMin,
						cfg.BinanceLeverageMax,
						cfg.BinanceLeverageDynamic,
					)

					if cfg.BinanceLeverageDynamic {
						log.Info(fmt.Sprintf("💡 LLM 选择杠杆: %dx (范围: %d-%d)", leverageToUse, cfg.BinanceLeverageMin, cfg.BinanceLeverageMax))
					} else {
						log.Info(fmt.Sprintf("💡 使用固定杠杆: %dx", leverageToUse))
					}

					// Calculate initial stop-loss if not provided by LLM
					// 如果 LLM 未提供止损价格，则计算初始止损
					initialStopLoss := symbolDecision.StopLoss
					if initialStopLoss == 0 {
						// Use 2.5% default stop-loss
						// 使用 2.5% 默认止损
						if symbolDecision.Action == executors.ActionBuy {
							initialStopLoss = result.Price * 0.975 // -2.5%
						} else {
							initialStopLoss = result.Price * 1.025 // +2.5%
						}
						log.Info(fmt.Sprintf("LLM 未提供止损价格，使用默认 2.5%% 止损: %.2f", initialStopLoss))
					}

					// Get ATR value from indicators for dynamic trailing stop
					// 从指标中获取 ATR 值用于动态追踪止损
					var atrValue float64
					reports := state.GetSymbolReports(symbol)
					if reports != nil && reports.TechnicalIndicators != nil {
						indicators := reports.TechnicalIndicators
						if len(indicators.ATR_7) > 0 {
							// Get latest ATR value
							// 获取最新 ATR 值
							lastIdx := len(indicators.ATR_7) - 1
							if lastIdx >= 0 && !math.IsNaN(indicators.ATR_7[lastIdx]) {
								atrValue = indicators.ATR_7[lastIdx]
								atrPercent := (atrValue / result.Price) * 100
								log.Info(fmt.Sprintf("当前 ATR: %.2f (%.2f%% of price)", atrValue, atrPercent))
							}
						}
					}

					// Create position
					// 创建持仓
					// Determine position side from action
					// 从动作确定持仓方向
					positionSide := "long"
					if symbolDecision.Action == executors.ActionSell {
						positionSide = "short"
					}

					position := &executors.Position{
						ID:              fmt.Sprintf("%s-%d", symbol, time.Now().Unix()),
						Symbol:          symbol,
						Side:            positionSide,
						EntryPrice:      result.Price,
						EntryTime:       time.Now(),
						Quantity:        result.Amount,
						Leverage:        leverageToUse,
						InitialStopLoss: initialStopLoss,
						CurrentStopLoss: initialStopLoss,
						StopLossType:    "fixed",
						OpenReason:      symbolDecision.Reason,
						ATR:             atrValue,
					}

					// Register to stop-loss manager
					// 注册到止损管理器
					globalStopLossManager.RegisterPosition(position)

					// Save position to database
					// 保存持仓到数据库
					posRecord := &storage.PositionRecord{
						ID:               position.ID,
						Symbol:           position.Symbol,
						Side:             position.Side,
						EntryPrice:       position.EntryPrice,
						EntryTime:        position.EntryTime,
						Quantity:         position.Quantity,
						Leverage:         position.Leverage,
						InitialStopLoss:  position.InitialStopLoss,
						CurrentStopLoss:  position.CurrentStopLoss,
						StopLossType:     position.StopLossType,
						TrailingDistance: position.TrailingDistance,
						HighestPrice:     position.EntryPrice,
						CurrentPrice:     position.EntryPrice,
						OpenReason:       position.OpenReason,
						ATR:              position.ATR,
						StopLossOrderID:  position.StopLossOrderID, // ✅ 保存止损单 ID
						Closed:           false,
					}
					if err := db.SavePosition(posRecord); err != nil {
						log.Warning(fmt.Sprintf("⚠️  保存持仓到数据库失败: %v", err))
					}

					// Place initial stop-loss order
					// 下初始止损单
					if err := globalStopLossManager.PlaceInitialStopLoss(ctx, position); err != nil {
						log.Warning(fmt.Sprintf("⚠️  下初始止损单失败: %v", err))
					} else {
						log.Success(fmt.Sprintf("✅ 初始止损单已下达: %.2f", initialStopLoss))
					}
				}
			} else {
				executionResults[symbol] = fmt.Sprintf("❌ 执行失败: %s", result.Message)
			}
		}

		// Update portfolio summary after execution
		// 执行后更新投资组合摘要
		log.Subheader("执行后投资组合状态", '─', 80)
		if err := portfolioMgr.UpdateBalance(ctx); err != nil {
			log.Warning(fmt.Sprintf("⚠️  获取更新后的余额失败: %v", err))
		}

		// Update positions for all symbols
		// 更新所有交易对的持仓信息
		for _, symbol := range cfg.CryptoSymbols {
			if err := portfolioMgr.UpdatePosition(ctx, symbol); err != nil {
				log.Warning(fmt.Sprintf("⚠️  获取 %s 持仓信息失败: %v", symbol, err))
			}
		}

		log.Info(portfolioMgr.GetPortfolioSummary())

		// Save balance history to database
		// 保存余额历史到数据库
		balanceHistory := &storage.BalanceHistory{
			Timestamp:        time.Now(),
			TotalBalance:     portfolioMgr.GetTotalBalance(),
			AvailableBalance: portfolioMgr.GetAvailableBalance(),
			UnrealizedPnL:    portfolioMgr.GetTotalUnrealizedPnL(),
			Positions:        portfolioMgr.GetPositionCount(),
		}
		if err := db.SaveBalanceHistory(balanceHistory); err != nil {
			log.Warning(fmt.Sprintf("⚠️  保存余额历史失败: %v", err))
		}

		// Display execution summary
		// 显示执行摘要
		log.Subheader("执行结果摘要", '─', 80)
		for symbol, result := range executionResults {
			log.Info(fmt.Sprintf("【%s】%s", symbol, result))
		}

		// Build execution result string
		// 构建执行结果字符串
		var resultBuilder strings.Builder
		for symbol, result := range executionResults {
			resultBuilder.WriteString(fmt.Sprintf("%s: %s\n", symbol, result))
		}

		// Update database with execution results
		// 更新数据库中的执行结果
		log.Info("更新数据库执行记录...")
		executionResultStr := resultBuilder.String()
		for _, symbol := range cfg.CryptoSymbols {
			if err := db.UpdateLatestSessionExecution(symbol, cfg.CryptoTimeframe, true, executionResultStr); err != nil {
				log.Warning(fmt.Sprintf("⚠️  更新 %s 执行记录失败: %v", symbol, err))
			}
		}

		log.Success("✅ 自动执行流程完成")
	} else {
		log.Info("💤 自动执行模式未启用 (设置 AUTO_EXECUTE=true 以启用)")
	}

	log.Success("✅ 本次执行完成")
	return nil
}
