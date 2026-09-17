// Command uipreview boots the real web dashboard against a seeded in-memory
// dataset so the UI can be reviewed without Binance or LLM credentials.
//
// cmd/web/main.go exits if it cannot reach the LLM backend and Binance, which
// makes it unusable for a pure front-end review. This harness wires up the
// identical internal/web.Server — same routes, same templates, same handlers —
// but skips the trading loop entirely. It never places an order.
package main

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/oak/crypto-trading-bot/internal/config"
	"github.com/oak/crypto-trading-bot/internal/logger"
	"github.com/oak/crypto-trading-bot/internal/scheduler"
	"github.com/oak/crypto-trading-bot/internal/storage"
	"github.com/oak/crypto-trading-bot/internal/web"
)

func main() {
	dbPath := "/tmp/uipreview/preview.db"
	if err := os.MkdirAll("/tmp/uipreview", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	_ = os.Remove(dbPath)

	log := logger.NewColorLogger(true)

	db, err := storage.NewStorage(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := seed(db); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}

	cfg := &config.Config{
		CryptoSymbols:          []string{"BTC/USDT", "ETH/USDT", "SOL/USDT"},
		CryptoTimeframe:        "3m",
		TradingInterval:        "15m",
		BinanceTestMode:        true,
		BinanceLeverageMin:     10,
		BinanceLeverageMax:     20,
		BinanceLeverageDynamic: true,
		AutoExecute:            true,
		DatabasePath:           dbPath,
		WebPort:                8080,
		WebUsername:            "admin",
		WebPassword:            "preview",
	}

	sched, err := scheduler.NewTradingScheduler(cfg.TradingInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: %v\n", err)
		os.Exit(1)
	}

	// The dashboard runs on an internal port; a thin proxy in front of it serves
	// demo data for the two endpoints that require a live Binance connection.
	// 仪表盘运行在内部端口上；前面的轻量代理为两个需要实时连接币安的接口提供演示数据。
	const internalPort = 8090
	cfg.WebPort = internalPort

	// stopLossManager is nil: every handler that touches it is nil-guarded.
	srv := web.NewServer(cfg, log, db, nil, sched)
	go srv.Start()

	fmt.Printf("UI preview on http://localhost:%d  (admin / preview)\n", publicPort)
	if err := serveProxy(publicPort, internalPort); err != nil {
		fmt.Fprintf(os.Stderr, "proxy: %v\n", err)
		os.Exit(1)
	}
}

// publicPort is the port the preview is actually browsed on.
// publicPort 是实际访问预览时使用的端口
const publicPort = 8080

// serveProxy forwards to the real dashboard, standing in for the two endpoints
// that need a live Binance account.
// serveProxy 将请求转发给真实仪表盘，只替换两个需要真实币安账户的接口
//
// Everything else — templates, routes, auth, the database — is the real server.
// Without this, the equity figure and the positions panel are empty on any
// machine that cannot reach Binance, which is most machines running a UI review.
// 其余部分（模板、路由、鉴权、数据库）都是真实服务。
// 若没有这层代理，在无法访问币安的机器上，权益数值与持仓面板都会是空的，
// 而做界面评审的机器大多属于此类。
func serveProxy(from, to int) error {
	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", to))
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	demo := map[string]string{
		"/api/balance/current": `{"total_balance":11450.02,"available_balance":8230.11,` +
			`"unrealized_pnl":236.58,"positions":2}`,
		"/api/positions/live": `{"positions":[` +
			`{"symbol":"BTCUSDT","side":"long","size":0.125,"entry_price":86420.50,` +
			`"current_price":88730.00,"unrealized_pnl":288.75,"roe":40.1,"leverage":15,` +
			`"liquidation_price":79100.00,"current_stop_loss":87310.00},` +
			`{"symbol":"SOLUSDT","side":"short","size":18.5,"entry_price":198.42,` +
			`"current_price":195.60,"unrealized_pnl":-52.17,"roe":-14.2,"leverage":10,` +
			`"liquidation_price":221.40,"current_stop_loss":196.85}],"count":2}`,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := demo[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, body)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	// Wait for the dashboard to bind before accepting traffic
	// 等待仪表盘完成端口绑定后再接收流量
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", target.Host, 200*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return http.ListenAndServe(fmt.Sprintf(":%d", from), mux)
}

// seed fills the database with plausible trading history so every page and
// empty state has something to render.
func seed(db *storage.Storage) error {
	symbols := []string{"BTC/USDT", "ETH/USDT", "SOL/USDT"}
	actions := []string{"BUY", "SELL", "HOLD", "CLOSE_LONG", "CLOSE_SHORT"}
	rng := rand.New(rand.NewSource(7))

	now := time.Now()

	// 24 batches, one every 15 minutes, three symbols each
	for b := 23; b >= 0; b-- {
		created := now.Add(-time.Duration(b) * 15 * time.Minute)
		batchID := fmt.Sprintf("batch-%d", created.Unix())

		for _, sym := range symbols {
			action := actions[rng.Intn(len(actions))]
			executed := action != "HOLD"

			session := &storage.TradingSession{
				BatchID:      batchID,
				Symbol:       sym,
				Timeframe:    "3m",
				CreatedAt:    created,
				MarketReport: marketReport(sym),
				CryptoReport: cryptoReport(sym),
				SentimentReport: "## 市场情绪\n\n- **Fear & Greed Index**: 62 (Greed)\n" +
					"- **社交热度**: 较昨日 +18%\n- **资金费率**: 0.0085% (多头拥挤)\n",
				PositionInfo: positionInfo(sym),
				Decision:     decision(sym, action),
				FullDecision: fullDecision(symbols, action),
				Executed:     executed,
				ExecutionResult: map[bool]string{
					true:  fmt.Sprintf("✅ 成功执行 %s", action),
					false: "观望，不执行交易",
				}[executed],
			}

			if _, err := db.SaveSession(session); err != nil {
				return err
			}
		}
	}

	// Balance curve: a week of 10-minute samples with a mild upward drift
	balance := 10000.0
	for i := 1008; i >= 0; i-- {
		ts := now.Add(-time.Duration(i) * 10 * time.Minute)
		balance += rng.NormFloat64()*35 + 2.2
		unrealized := math.Sin(float64(i)/40) * 120

		if err := db.SaveBalanceHistory(&storage.BalanceHistory{
			Timestamp:        ts,
			TotalBalance:     balance,
			AvailableBalance: balance * 0.72,
			UnrealizedPnL:    unrealized,
			Positions:        2,
		}); err != nil {
			return err
		}
	}

	// Two open positions, so the positions table has rows if a data source
	// provides them (the dashboard's live table reads from Binance).
	positions := []*storage.PositionRecord{
		{
			ID: "BTCUSDT-preview", Symbol: "BTCUSDT", Side: "long",
			EntryPrice: 86420.5, EntryTime: now.Add(-6 * time.Hour), Quantity: 0.125,
			Leverage: 15, InitialStopLoss: 84200.0, CurrentStopLoss: 87310.0,
			StopLossType: "trailing", HighestPrice: 89150.0, CurrentPrice: 88730.0,
			UnrealizedPnL: 288.75, OpenReason: "ADX 41 强上升趋势，EMA20 上穿 EMA50", ATR: 420.5,
		},
		{
			ID: "SOLUSDT-preview", Symbol: "SOLUSDT", Side: "short",
			EntryPrice: 198.42, EntryTime: now.Add(-2 * time.Hour), Quantity: 18.5,
			Leverage: 10, InitialStopLoss: 204.90, CurrentStopLoss: 202.15,
			StopLossType: "trailing", HighestPrice: 193.10, CurrentPrice: 195.60,
			UnrealizedPnL: -52.17, OpenReason: "RSI 背离 + 4h 阻力位受阻", ATR: 3.8,
		},
	}
	for _, p := range positions {
		if err := db.SavePosition(p); err != nil {
			return err
		}
	}

	// Closed trades shaped like the strategy this bot actually runs: a low win
	// rate carried by a high payoff ratio. Without these the discipline panel
	// has nothing to show.
	// 已平仓交易按本机器人实际运行的策略塑造：低胜率、高盈亏比。
	// 没有这些数据，纪律面板就无内容可展示。
	closed := []float64{412.80, -96.40, -88.15, 733.20, -102.60, -91.05, 268.45, -84.30, -110.20, 519.60, -97.85, -93.40, 344.10}
	for i, pnl := range closed {
		openedAt := now.Add(-time.Duration(len(closed)-i) * 9 * time.Hour)
		sym := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}[i%3]
		side := "long"
		if i%4 == 1 {
			side = "short"
		}

		closedAt := openedAt.Add(6 * time.Hour)
		rec := &storage.PositionRecord{
			ID:              fmt.Sprintf("%s-closed-%d", sym, i),
			Symbol:          sym,
			Side:            side,
			EntryPrice:      80000 + float64(i)*120,
			EntryTime:       openedAt,
			Quantity:        0.1,
			Leverage:        15,
			InitialStopLoss: 78000,
			CurrentStopLoss: 78900,
			StopLossType:    "trailing",
			HighestPrice:    82000,
			CurrentPrice:    81500,
			OpenReason:      "趋势确认后入场",
			Closed:          true,
			CloseTime:       &closedAt,
			ClosePrice:      81500,
			CloseReason:     "追踪止损触发",
			RealizedPnL:     pnl,
		}
		// Mirror the real flow: SavePosition records the open, UpdatePosition
		// writes the close fields. Insert alone leaves realized_pnl NULL.
		// 复现真实流程：SavePosition 记录开仓，UpdatePosition 写入平仓字段。
		// 只做插入会让 realized_pnl 保持为 NULL。
		if err := db.SavePosition(rec); err != nil {
			return err
		}
		if err := db.UpdatePosition(rec); err != nil {
			return err
		}
	}

	return nil
}

func marketReport(sym string) string {
	return fmt.Sprintf(`## %s 技术指标分析

### 多时间框架指标

| 周期 | EMA20 | EMA50 | MACD | RSI7 | RSI14 |
|------|-------|-------|------|------|-------|
| 3分钟 | 86014.07 | 86066.33 | -28.44 | 48.63 | 51.64 |
| 15分钟 | 86217.47 | 86549.92 | -216.44 | 50.81 | 51.48 |
| 1小时 | 85980.12 | 85102.44 | 412.09 | 61.22 | 58.03 |
| 4小时 | 84220.55 | 82640.18 | 1180.77 | 66.40 | 62.91 |

### 趋势判断

- **ADX(14)**: 41.01 — 强趋势
- **布林带**: 价格运行于中轨上方，带宽扩张
- **ATR(7)**: 420.50 (0.49%% of price)

> 结论：短周期回调，中长周期趋势完好，属于**趋势中的健康回踩**。

`+"```"+`
支撑位: 85,200 / 84,100
阻力位: 88,900 / 90,500
`+"```", sym)
}

func cryptoReport(sym string) string {
	return fmt.Sprintf(`## %s 链上与合约数据

- **未平仓合约 (OI)**: 82.4 亿美元，24h +3.2%%
- **多空持仓人数比**: 1.84
- **资金费率**: 0.0085%%（8h）
- **买卖盘深度**: 买盘 1,240 万 / 卖盘 980 万

买盘深度明显占优，短期下行空间受限。`, sym)
}

func positionInfo(sym string) string {
	return fmt.Sprintf(`## 当前持仓 (%s)

- 方向: 多头
- 数量: 0.1250
- 开仓价格: $86,420.50
- 当前价格: $88,730.00
- 未实现盈亏: **+288.75 USDT**
- 当前止损: $87,310.00
- 杠杆: 15x`, sym)
}

func decision(sym, action string) string {
	return fmt.Sprintf(`【%s】
**交易方向**: %s
**置信度**: 0.78
**杠杆倍数**: 15倍
**仓位建议**: 22%%
**止损价格**: $84,200.00
**入场理由**: ADX 41.01 显示强上升趋势，1h/4h EMA 多头排列，回调至 EMA20 获得支撑。
**风险提示**: 若跌破 84,200 则趋势判断失效，立即止损离场。`, sym, action)
}

func fullDecision(symbols []string, action string) string {
	out := "# LLM 交易决策（全部交易对）\n\n"
	for _, s := range symbols {
		out += decision(s, action) + "\n\n---\n\n"
	}
	out += "## 组合层面风险控制\n\n" +
		"- 当前总敞口占用保证金 **28%**，低于 30% 上限\n" +
		"- 三个交易对相关性较高，实际风险敞口按 1.6 倍折算\n" +
		"- 建议：**不再新增同向仓位**，等待现有持仓走出趋势\n"
	return out
}
