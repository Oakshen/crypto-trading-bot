package executors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

// AccountBalance is the subset of futures account data this bot actually uses.
// AccountBalance 是本机器人实际使用的期货账户数据子集
//
// Exposing our own struct instead of the SDK's keeps callers insulated from
// Binance's account endpoint versioning.
// 暴露自定义结构体而非 SDK 类型，使调用方不受币安账户接口版本变化的影响。
type AccountBalance struct {
	WalletBalance    float64 // 钱包余额 / Wallet balance
	AvailableBalance float64 // 可用余额 / Available balance
	UnrealizedProfit float64 // 未实现盈亏 / Unrealized PnL
}

// symbolConfigTTL bounds how long a cached leverage/margin-type lookup is reused.
// symbolConfigTTL 限制杠杆/保证金模式缓存的复用时长
//
// Short, because the user can change leverage in the Binance UI at any time.
// 设置得较短，因为用户随时可能在币安界面上修改杠杆。
const symbolConfigTTL = 60 * time.Second

// serverTimeSyncInterval is how often the SDK clock offset is re-synced.
// serverTimeSyncInterval 是重新同步 SDK 时钟偏移的间隔
const serverTimeSyncInterval = 30 * time.Minute

type cachedSymbolConfig struct {
	cfg       *futures.SymbolConfig
	fetchedAt time.Time
}

// symbolConfigCache caches GET /fapi/v1/symbolConfig responses per symbol.
// symbolConfigCache 按交易对缓存 GET /fapi/v1/symbolConfig 的响应
//
// /fapi/v3/positionRisk no longer returns leverage or marginType, so those now
// come from symbolConfig. Caching keeps the extra signed call off the hot path.
// /fapi/v3/positionRisk 不再返回 leverage 和 marginType，因此改由 symbolConfig 提供。
// 加缓存可以避免这次额外的签名调用出现在高频路径上。
type symbolConfigCache struct {
	mu      sync.RWMutex
	entries map[string]cachedSymbolConfig
}

func newSymbolConfigCache() *symbolConfigCache {
	return &symbolConfigCache{entries: make(map[string]cachedSymbolConfig)}
}

// GetSymbolConfig returns leverage / margin type for a symbol.
// GetSymbolConfig 返回交易对的杠杆与保证金模式
func (e *BinanceExecutor) GetSymbolConfig(ctx context.Context, symbol string) (*futures.SymbolConfig, error) {
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)

	e.symbolConfigs.mu.RLock()
	entry, ok := e.symbolConfigs.entries[binanceSymbol]
	e.symbolConfigs.mu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < symbolConfigTTL {
		return entry.cfg, nil
	}

	configs, err := e.client.NewGetSymbolConfigService().Symbol(binanceSymbol).Do(ctx)
	if err != nil {
		// Serving a stale value beats failing a trade over a transient error.
		// 返回过期值也好过因为一次瞬时错误导致交易失败。
		if ok {
			return entry.cfg, nil
		}
		return nil, fmt.Errorf("获取 %s 交易对配置失败: %w", binanceSymbol, err)
	}

	for _, cfg := range configs {
		if cfg.Symbol == binanceSymbol {
			e.symbolConfigs.mu.Lock()
			e.symbolConfigs.entries[binanceSymbol] = cachedSymbolConfig{cfg: cfg, fetchedAt: time.Now()}
			e.symbolConfigs.mu.Unlock()
			return cfg, nil
		}
	}

	return nil, fmt.Errorf("未找到 %s 的交易对配置", binanceSymbol)
}

// invalidateSymbolConfig drops a cached entry after we change leverage ourselves.
// invalidateSymbolConfig 在我们主动修改杠杆后丢弃对应缓存
func (e *BinanceExecutor) invalidateSymbolConfig(symbol string) {
	binanceSymbol := e.config.GetBinanceSymbolFor(symbol)
	e.symbolConfigs.mu.Lock()
	delete(e.symbolConfigs.entries, binanceSymbol)
	e.symbolConfigs.mu.Unlock()
}

// GetAccountBalance returns the USDT balance via GET /fapi/v3/account.
// GetAccountBalance 通过 GET /fapi/v3/account 返回 USDT 余额
//
// v3 replaces the deprecated /fapi/v2/account and only returns symbols that
// actually have a position or open order, so it is both current and cheaper.
// v3 取代了已弃用的 /fapi/v2/account，并且只返回真正有持仓或挂单的交易对，
// 既是最新接口，权重也更低。
func (e *BinanceExecutor) GetAccountBalance(ctx context.Context) (*AccountBalance, error) {
	account, err := e.client.NewGetAccountV3Service().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取账户信息失败: %w", err)
	}

	for _, asset := range account.Assets {
		if asset.Asset != "USDT" {
			continue
		}
		balance := &AccountBalance{}
		balance.WalletBalance, _ = parseFloat(asset.WalletBalance)
		balance.AvailableBalance, _ = parseFloat(asset.AvailableBalance)
		balance.UnrealizedProfit, _ = parseFloat(asset.UnrealizedProfit)
		return balance, nil
	}

	return nil, fmt.Errorf("账户中未找到 USDT 资产")
}

// syncServerTime refreshes the SDK's clock offset against Binance.
// syncServerTime 根据币安服务器时间刷新 SDK 的时钟偏移
//
// Without this a long-running process drifts and every signed request starts
// failing with -1021 (timestamp outside recvWindow).
// 没有它，长期运行的进程时钟会漂移，之后每个签名请求都会因 -1021
// （时间戳超出 recvWindow）而失败。
func (e *BinanceExecutor) syncServerTime(ctx context.Context) {
	e.timeSyncMu.Lock()
	defer e.timeSyncMu.Unlock()

	if !e.lastTimeSync.IsZero() && time.Since(e.lastTimeSync) < serverTimeSyncInterval {
		return
	}

	offset, err := e.client.NewSetServerTimeService().Do(ctx)
	if err != nil {
		e.logger.Warning(fmt.Sprintf("⚠️  同步币安服务器时间失败: %v（继续使用本地时间）", err))
		return
	}

	e.lastTimeSync = time.Now()
	if offset > 1000 || offset < -1000 {
		e.logger.Warning(fmt.Sprintf("⚠️  本地时钟与币安相差 %d ms，已自动校正", offset))
	}
}

// SymbolFilters returns the exchange-defined trading rules for a symbol.
// SymbolFilters 返回交易对在交易所侧定义的交易规则
func (e *BinanceExecutor) SymbolFilters(ctx context.Context, symbol string) (*SymbolFilters, error) {
	return e.symbolInfo.Get(ctx, e.config.GetBinanceSymbolFor(symbol))
}

// Init warms up the caches that order placement depends on.
// Init 预热下单所依赖的缓存
//
// Call once at startup so the first trade does not pay for exchangeInfo.
// 在启动时调用一次，使第一笔交易不必承担拉取 exchangeInfo 的开销。
func (e *BinanceExecutor) Init(ctx context.Context) error {
	e.syncServerTime(ctx)

	if err := e.symbolInfo.Refresh(ctx); err != nil {
		return fmt.Errorf("加载交易规则失败: %w", err)
	}

	e.logger.Success("交易规则 (exchangeInfo) 已加载")
	return nil
}
