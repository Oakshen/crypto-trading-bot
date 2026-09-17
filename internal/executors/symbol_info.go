package executors

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

// SymbolFilters holds the exchange-defined trading rules for one symbol.
// SymbolFilters 保存币安为单个交易对定义的交易规则
//
// These values come from GET /fapi/v1/exchangeInfo and MUST be used to format
// every quantity and price we send. Hardcoded precision tables go stale as soon
// as Binance re-lists a contract, and a wrong number of decimals is rejected
// with -1111 or silently distorts a stop-loss level.
// 这些值来自 GET /fapi/v1/exchangeInfo，必须用它们格式化我们发送的每一个数量和价格。
// 硬编码的精度表在币安调整合约后立刻失效，小数位错误会导致 -1111，
// 或者更糟：悄无声息地把止损价改到错误的位置。
type SymbolFilters struct {
	Symbol      string  // 交易对 / Trading pair
	TickSize    float64 // 价格最小变动单位 / Minimum price increment
	StepSize    float64 // 数量最小变动单位 / Minimum quantity increment
	MinQty      float64 // 最小下单数量 / Minimum order quantity
	MaxQty      float64 // 最大下单数量 / Maximum order quantity
	MinNotional float64 // 最小订单价值 / Minimum order notional value

	priceDecimals int // 由 TickSize 推导 / Derived from TickSize
	qtyDecimals   int // 由 StepSize 推导 / Derived from StepSize
}

// SymbolInfoCache fetches and caches exchange info for all symbols.
// SymbolInfoCache 获取并缓存所有交易对的交易规则
//
// exchangeInfo is a single unauthenticated call that returns every symbol, so we
// fetch it once and refresh it on a TTL rather than per order.
// exchangeInfo 是一次免签名调用即可返回全部交易对的接口，
// 因此按 TTL 整体刷新，而不是每次下单都请求。
type SymbolInfoCache struct {
	mu       sync.RWMutex
	client   *futures.Client
	filters  map[string]*SymbolFilters
	loadedAt time.Time
	ttl      time.Duration
}

// symbolInfoTTL is how long cached exchange info stays valid.
// symbolInfoTTL 是缓存的交易规则的有效期
const symbolInfoTTL = 6 * time.Hour

// NewSymbolInfoCache creates a new exchange-info cache.
// NewSymbolInfoCache 创建新的交易规则缓存
func NewSymbolInfoCache(client *futures.Client) *SymbolInfoCache {
	return &SymbolInfoCache{
		client:  client,
		filters: make(map[string]*SymbolFilters),
		ttl:     symbolInfoTTL,
	}
}

// Refresh reloads exchange info from Binance.
// Refresh 从币安重新加载交易规则
func (c *SymbolInfoCache) Refresh(ctx context.Context) error {
	info, err := c.client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return fmt.Errorf("获取 exchangeInfo 失败: %w", err)
	}

	parsed := make(map[string]*SymbolFilters, len(info.Symbols))
	for i := range info.Symbols {
		s := &info.Symbols[i]
		f := &SymbolFilters{Symbol: s.Symbol}

		if pf := s.PriceFilter(); pf != nil {
			f.TickSize, _ = strconv.ParseFloat(pf.TickSize, 64)
		}
		if lf := s.LotSizeFilter(); lf != nil {
			f.StepSize, _ = strconv.ParseFloat(lf.StepSize, 64)
			f.MinQty, _ = strconv.ParseFloat(lf.MinQuantity, 64)
			f.MaxQty, _ = strconv.ParseFloat(lf.MaxQuantity, 64)
		}

		// Market orders are additionally bounded by MARKET_LOT_SIZE; take the
		// stricter of the two so a market order is never rejected.
		// 市价单还受 MARKET_LOT_SIZE 限制；取两者中更严格的值，避免市价单被拒。
		if mf := s.MarketLotSizeFilter(); mf != nil {
			if minQty, err := strconv.ParseFloat(mf.MinQuantity, 64); err == nil && minQty > f.MinQty {
				f.MinQty = minQty
			}
			if maxQty, err := strconv.ParseFloat(mf.MaxQuantity, 64); err == nil && maxQty > 0 {
				if f.MaxQty == 0 || maxQty < f.MaxQty {
					f.MaxQty = maxQty
				}
			}
		}

		if nf := s.MinNotionalFilter(); nf != nil {
			f.MinNotional, _ = strconv.ParseFloat(nf.Notional, 64)
		}

		// Prefer decimals implied by tick/step size; fall back to the precision
		// fields when a filter is missing.
		// 优先使用 tick/step 推导出的小数位；filter 缺失时回退到 precision 字段。
		f.priceDecimals = decimalsOf(f.TickSize, s.PricePrecision)
		f.qtyDecimals = decimalsOf(f.StepSize, s.QuantityPrecision)

		parsed[s.Symbol] = f
	}

	if len(parsed) == 0 {
		return fmt.Errorf("exchangeInfo 返回空的交易对列表")
	}

	c.mu.Lock()
	c.filters = parsed
	c.loadedAt = time.Now()
	c.mu.Unlock()

	return nil
}

// Get returns the filters for a symbol, refreshing the cache when needed.
// Get 返回交易对的规则，必要时刷新缓存
//
// It deliberately returns an error instead of guessing: sending a wrongly
// rounded price is worse than not sending the order at all.
// 这里刻意返回错误而不是猜测默认值：发送一个舍入错误的价格，
// 比不发送这个订单更危险。
func (c *SymbolInfoCache) Get(ctx context.Context, symbol string) (*SymbolFilters, error) {
	binanceSymbol := normalizeSymbol(symbol)

	c.mu.RLock()
	f, ok := c.filters[binanceSymbol]
	fresh := time.Since(c.loadedAt) < c.ttl
	c.mu.RUnlock()

	if ok && fresh {
		return f, nil
	}

	if err := c.Refresh(ctx); err != nil {
		// A stale entry still beats no entry at all.
		// 过期的缓存仍好过完全没有数据。
		if ok {
			return f, nil
		}
		return nil, err
	}

	c.mu.RLock()
	f, ok = c.filters[binanceSymbol]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("交易对 %s 不存在于币安合约列表中", binanceSymbol)
	}
	return f, nil
}

// FormatQuantity floors a quantity onto the step size and renders it with the
// exact number of decimals Binance expects.
// FormatQuantity 将数量向下取整到 StepSize，并按币安要求的小数位格式化
//
// Flooring (not rounding) guarantees we never exceed the size we intended to
// trade, which matters when closing a position.
// 向下取整（而非四舍五入）保证不会超出预期交易量，这在平仓时尤其重要。
func (f *SymbolFilters) FormatQuantity(qty float64) string {
	return strconv.FormatFloat(f.AdjustQuantity(qty), 'f', f.qtyDecimals, 64)
}

// AdjustQuantity floors a quantity onto the step size.
// AdjustQuantity 将数量向下取整到 StepSize
func (f *SymbolFilters) AdjustQuantity(qty float64) float64 {
	if f.StepSize <= 0 {
		return qty
	}
	// The epsilon absorbs float noise so 0.3/0.1 does not floor to 2 steps.
	// It scales with the magnitude of the result for the same reason as in
	// AdjustPrice.
	// epsilon 用于吸收浮点误差，避免 0.3/0.1 被取整成 2 步。
	// 与 AdjustPrice 同理，容差随结果量级缩放。
	steps := qty / f.StepSize
	return math.Floor(steps+1e-9*math.Max(1, math.Abs(steps))) * f.StepSize
}

// ValidateQuantity checks a quantity against the lot-size bounds.
// ValidateQuantity 按照数量限制校验下单量
func (f *SymbolFilters) ValidateQuantity(qty float64) error {
	adjusted := f.AdjustQuantity(qty)
	if f.MinQty > 0 && adjusted < f.MinQty {
		return fmt.Errorf("数量 %s 低于 %s 的最小下单量 %s",
			trimFloat(adjusted), f.Symbol, trimFloat(f.MinQty))
	}
	if f.MaxQty > 0 && adjusted > f.MaxQty {
		return fmt.Errorf("数量 %s 超过 %s 的单笔最大下单量 %s",
			trimFloat(adjusted), f.Symbol, trimFloat(f.MaxQty))
	}
	return nil
}

// PriceRounding selects how a price is snapped onto the tick size.
// PriceRounding 选择价格对齐到 TickSize 的方式
type PriceRounding int

const (
	RoundNearest PriceRounding = iota // 就近取整 / Round to nearest tick
	RoundDown                         // 向下取整 / Round down to tick
	RoundUp                           // 向上取整 / Round up to tick
)

// FormatPrice snaps a price onto the tick size and renders it with the exact
// number of decimals Binance expects.
// FormatPrice 将价格对齐到 TickSize，并按币安要求的小数位格式化
func (f *SymbolFilters) FormatPrice(price float64, mode PriceRounding) string {
	return strconv.FormatFloat(f.AdjustPrice(price, mode), 'f', f.priceDecimals, 64)
}

// AdjustPrice snaps a price onto the tick size.
// AdjustPrice 将价格对齐到 TickSize
func (f *SymbolFilters) AdjustPrice(price float64, mode PriceRounding) float64 {
	if f.TickSize <= 0 {
		return price
	}
	ticks := price / f.TickSize

	// Dividing by the tick size loses precision: 60123.45/0.1 evaluates to
	// 601234.4999999999, so a naive Round lands one tick low. The tolerance is
	// scaled to the magnitude of the result, because the absolute float error
	// grows with it.
	// 除以 tickSize 会损失精度：60123.45/0.1 的结果是 601234.4999999999，
	// 直接 Round 会低一个 tick。容差按结果量级缩放，
	// 因为浮点绝对误差会随量级增大。
	epsilon := 1e-9 * math.Max(1, math.Abs(ticks))

	switch mode {
	case RoundDown:
		ticks = math.Floor(ticks + epsilon)
	case RoundUp:
		ticks = math.Ceil(ticks - epsilon)
	default:
		ticks = math.Round(ticks + math.Copysign(epsilon, ticks))
	}
	return ticks * f.TickSize
}

// FormatStopPrice snaps a stop-loss trigger price away from the market.
// FormatStopPrice 将止损触发价朝远离市价的方向对齐
//
// Rounding a long's stop down and a short's stop up keeps the trigger on the
// safe side of the tick, so tick alignment can never pull the stop across the
// current price and fire it immediately.
// 多仓止损向下取整、空仓止损向上取整，使触发价停在 tick 的安全一侧，
// 这样对齐操作永远不会把止损价推过当前市价而导致立即触发。
func (f *SymbolFilters) FormatStopPrice(price float64, positionSide string) string {
	mode := RoundDown // long position -> SELL stop below market / 多仓止损在市价下方
	if strings.EqualFold(positionSide, "short") {
		mode = RoundUp // short position -> BUY stop above market / 空仓止损在市价上方
	}
	return f.FormatPrice(price, mode)
}

// PriceDecimals returns the number of decimals used for prices.
// PriceDecimals 返回价格使用的小数位数
func (f *SymbolFilters) PriceDecimals() int { return f.priceDecimals }

// QuantityDecimals returns the number of decimals used for quantities.
// QuantityDecimals 返回数量使用的小数位数
func (f *SymbolFilters) QuantityDecimals() int { return f.qtyDecimals }

// decimalsOf derives the decimal places implied by a tick/step size.
// decimalsOf 根据 tick/step 推导出小数位数
//
// Binance reports sizes as "0.00100000", which means 3 decimals, not 8.
// Sending more decimals than that is rejected with -1111.
// 币安返回的形如 "0.00100000" 表示 3 位小数而非 8 位，多发小数位会触发 -1111。
func decimalsOf(size float64, fallback int) int {
	if size <= 0 {
		return fallback
	}
	s := strconv.FormatFloat(size, 'f', -1, 64)
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		return 0
	}
	return len(strings.TrimRight(s[dot+1:], "0"))
}

// trimFloat renders a float without trailing zeros, for log messages.
// trimFloat 去掉末尾多余的零，用于日志输出
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// normalizeSymbol converts "BTC/USDT" to the Binance form "BTCUSDT".
// normalizeSymbol 将 "BTC/USDT" 转换为币安格式 "BTCUSDT"
func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.ReplaceAll(symbol, "/", ""))
}
