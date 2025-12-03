# 🤖 Crypto Trading Bot (Go Version)

[English](README_EN.md) | **简体中文**

---

基于 AI 智能体的加密货币自动交易系统 - **Go 语言实现版本**

使用大语言模型（LLM）分析市场数据、生成交易信号并在币安期货上执行交易。采用 **Cloudwego Eino 框架**进行多智能体并行编排。

![Trading Bot Dashboard](assets/fig7.png)

> ⚠️ **重要提示**：此项目从 Python 完全重构为 Go 版本，性能更高、并发能力更强。

---

## 一点感想
我首先抛出一个问题：为什么 LLM 在加密货币的交易中要**优于**我们自己操作？我想有下面这几点
1. **纪律性**：币圈起起伏伏，人容易受到情绪影响，而 LLM 写到完 Prompt 之后几乎是按照你设定的规则去行动(虽然有偶尔幻觉的存在)
2. **多币种处理**：LLM 可以同时分析多个交易对、多个时间周期的数据，而人很难做到这一点
3. **持续工作**：LLM 可以 24/7 不间断地分析市场，这段时间我在测试系统的时候已经习惯每天早上起来查看币安的收益(那种刺激)

不同的 LLM 的在量化市场中的表现都不尽相同，相信大家都是看了 nof1 的炒币大赛才来玩这个，表现最好的是 `Qwen3-max`还有 `DeepSeek`，那么我们应该去研究到底是什么让他们表现更好？

我看了[Nof1交易分析](https://nof1.kcores.com/trading_summary_cards.html),深受启发，觉得`Qwen3-max`和 `DeepSeek`收益好主要有以下几点
1. **极致的选择性**：Qwen3-max 在整个11天的测试中只交易了 **37** 次，DeepSeek 是 **55** 次，而表现最差的 gemini 2.5-pro 和 gpt5.1 分别是 **237** 次和 **135** 次；总结一下 Qwen3-max 和 DeepSeek 和 **武林高手**一样，该出手时就出手，而gemini 2.5-pro 和 gpt5.1这哼哈二将就像我们小散户一样
2. **高盈亏比交易**： Qwen3-max 的平均盈亏比是 **2.03:1** ，而表现差的 LLM 平均盈亏比都在 **1.2:1** 以下
3. **胜率不是决定因素**: Qwen3-max 的胜率只有 **27%** ，DeepSeek 是 **38.2%** ，而表现差的 gpt5.1 胜率是 **51.1%** 说明胜率并不是决定盈利的关键，关键是要有**高盈亏比**和**极致选择性**
4. **耐心持有**：如果你仔细分析了[nof1的交易记录](https://nof1.kcores.com/trading_timeline_with_btc.html)，你会发现 Qwen3-max 和 DeepSeek 的持仓时间都很长，基本上都是在趋势中持有，而不是频繁进出



基于以上的分析，我设计了这个项目，具体的交易 prompt 可以看 `prompts/trader_json_no_trailing_stop.txt`，

总结来说就是**趋势交易**，**极致选择性**，**高盈亏比**，**耐心持有**，希望大家在使用这个项目的时候也能遵循这些原则。

另外我没有设计**止盈**的功能，目前只有**追踪止损**，通过 ATR 公式来动态的调整止损，核心思想还是遵循：**让利润奔跑**。

【追踪止损例子】：
- 初始止损：60400（-2.5%）
- 涨到 65000：止损移至 63375（保本以上）
- 涨到 70000：止损移至 68250（+10%）
- 涨到 73000：止损移至 71175（+14.8%）
- 回调到 71000：触发止损，获利 9000 USDT





---

## 🏗️ 技术栈

- **语言**：Go 1.21+
- **工作流编排**：[Cloudwego Eino](https://github.com/cloudwego/eino)
- **Web 框架**：[Hertz](https://github.com/cloudwego/hertz)
- **交易所 API**：[go-binance](https://github.com/adshao/go-binance)
- **配置管理**：[Viper](https://github.com/spf13/viper)
- **日志**：[zerolog](https://github.com/rs/zerolog)
- **数据库**：SQLite3

---

## 🚀 快速开始

### ⚠️⚠️⚠️ 注意 ! ! !
**此项目最佳使用模式为：**

- **仓位模式**：单向模式
- **保证金模式**：全仓
- **设置动态杠杆范围**：如 `10-20`

### 前置要求

- **Go 1.21 或更高版本**
- 币安期货账户
- OpenAI 兼容 API Key（OpenAI、DeepSeek、Qwen等）

### 安装

```bash
# 克隆项目
git clone https://github.com/Oakshen/crypto-trading-bot.git
cd crypto-trading-bot

# 安装依赖
make deps

# 编译所有组件
make build-all
```

### 配置

1. 复制配置文件模板：
```bash
cp .env.example .env
```

2. 编辑 `.env` 文件，配置必要参数：

```env
# ===================================================================
# LLM 配置（OpenAI 兼容 API）
# ===================================================================
LLM_PROVIDER=openai
DEEP_THINK_LLM=deepseek-reasoner      # 用于最终交易决策
QUICK_THINK_LLM=deepseek-chat         # 用于数据分析
LLM_BACKEND_URL=https://api.deepseek.com
OPENAI_API_KEY=你的-api-key

# 交易策略 Prompt
TRADER_PROMPT_PATH=prompts/trader_json_no_trailing_stop.txt

# ===================================================================
# 币安交易配置
# ===================================================================
BINANCE_API_KEY=你的币安API密钥
BINANCE_API_SECRET=你的币安API密钥

# 代理（可选，无法直接访问币安的用户需要）
# BINANCE_PROXY=http://192.168.0.226:6152

# 动态杠杆（推荐）
BINANCE_LEVERAGE=5-15  # LLM 根据置信度在 10-20 倍范围内选择

# 持仓模式（重要：使用单向持仓模式）
BINANCE_POSITION_MODE=oneway  # 选项：oneway（推荐）、hedge、auto

# ===================================================================
# 交易参数
# ===================================================================
# 交易对（支持多个）
CRYPTO_SYMBOLS=BTC/USDT,ETH/USDT,SOL/USDT

# K 线数据间隔（用于计算技术指标）
CRYPTO_TIMEFRAME=15m

# 系统运行间隔（多久运行一次分析）
TRADING_INTERVAL=15m

# ⭐ 最佳实践：
#   - 精细 K 线（15m）+ 低频决策（15m）
#   - 更精确的技术指标，同时避免过度交易
#   - 示例：CRYPTO_TIMEFRAME=15m, TRADING_INTERVAL=15m

# ===================================================================
# 多时间周期分析（推荐）
# ===================================================================
ENABLE_MULTI_TIMEFRAME=true
CRYPTO_LONGER_TIMEFRAME=1h  # 使用 4h 数据提供趋势背景

# ===================================================================
# 风险管理
# ===================================================================
ENABLE_STOPLOSS=true  # 启用 LLM 驱动的止损管理

# 情绪分析（不推荐 - 延迟大、价值低）
ENABLE_SENTIMENT_ANALYSIS=false

# ===================================================================
# 执行模式（重要）
# ===================================================================
# ⚠️ 警告：先设置为 false，充分测试后再设置为 true
AUTO_EXECUTE=false  # 设置为 true 启用自动交易

# Web 监控
WEB_PORT=8080

# Web 密码
WEB_USERNAME=admin
WEB_PASSWORD=123456
```

### 运行

```bash
# 单次执行模式（运行一次分析后退出）
make run

# Web 监控模式（持续运行 + Web 界面）
make run-web

# 查询历史数据
make query ARGS="stats"                 # 查看统计信息
make query ARGS="latest 10"             # 最近 10 次会话
make query ARGS="symbol BTC/USDT 5"     # 特定交易对
```

Web 界面默认地址：`http://localhost:8080`

---

## 📖 使用指南

### 1. 新手推荐流程

**第一步：使用 AUTO_EXECUTE=false 测试**
```env
AUTO_EXECUTE=false
BINANCE_POSITION_MODE=oneway
```
运行 `make run-web`，观察 LLM 决策 1-2 天

**第二步：启用自动执行**
```env
AUTO_EXECUTE=true
```
密切监控，随时准备停止系统

**第三步：优化策略**
- 根据结果调整杠杆范围
- 在 `prompts/trader_json_no_trailing_stop.txt` 中微调交易 Prompt
- 监控余额曲线和持仓表现

### 2. 理解时间周期配置

**场景 1：标准模式**（K 线间隔 = 运行间隔）（⭐ 推荐）
```env
CRYPTO_TIMEFRAME=15m
TRADING_INTERVAL=15m  # （或省略，默认使用 CRYPTO_TIMEFRAME）
```
结果：每 15 分钟获取 15 分钟 K 线数据

**场景 2：精细 K 线 + 低频决策**
```env
CRYPTO_TIMEFRAME=3m      # 基于 3 分钟 K 线计算指标
TRADING_INTERVAL=15m     # 每 15 分钟做一次决策
```
好处：
- 更精确的技术指标（EMA、MACD、RSI 基于 3m 数据）
- 避免过度交易（仅每 15 分钟决策一次）
- 兼得精确性与耐心

**场景 3：不推荐**（K 线间隔 > 运行间隔）
```env
CRYPTO_TIMEFRAME=1h
TRADING_INTERVAL=15m
```
问题：每 15 分钟运行但 1 小时 K 线未更新，浪费 API 调用

### 3. 自定义交易策略

编辑 `prompts/trader_optimized.txt` 修改交易策略，无需重新编译：

```bash
# 使用不同的 Prompt 文件
TRADER_PROMPT_PATH=prompts/trader_aggressive.txt 
```

提供的策略模板：
- `trader_optimized.txt` - 趋势交易，极度选择性（推荐）
- `trader_system.txt` - 趋势交易，平衡方法
- `trader_aggressive.txt` - 短线交易，积极捕捉机会

### 4. 多交易对配置

```bash
# 同时监控多个交易对
CRYPTO_SYMBOLS=BTC/USDT,ETH/USDT,SOL/USDT

# 系统会并行分析，选择最优机会
# 建议：不要超过 3 个交易对，避免过度分散
```

### 5. 查看实时数据

```bash
# Web API 端点
curl http://localhost:8080/api/balance/current    # 实时余额
curl http://localhost:8080/api/balance/history    # 余额历史
curl http://localhost:8080/api/positions          # 当前持仓
```

---

## 📁 项目结构

```
crypto-trading-bot/
├── cmd/
│   ├── main.go           # 单次执行模式入口
│   ├── web/main.go       # Web 监控模式入口
│   └── query/main.go     # 数据查询工具
├── internal/
│   ├── agents/           # AI 智能体（Eino Graph 工作流）
│   ├── dataflows/        # 市场数据获取和指标计算
│   ├── executors/        # 交易执行和止损管理
│   ├── portfolio/        # 投资组合管理
│   ├── storage/          # SQLite 数据库
│   ├── scheduler/        # 时间调度器
│   ├── web/              # Web 服务器和模板
│   ├── config/           # 配置加载
│   └── logger/           # 日志系统
├── prompts/              # 外部 Prompt 文件
├── data/                 # SQLite 数据库文件
├── .env.example          # 配置文件模板
├── Makefile              # 构建脚本
└── README.md
```

---

## 🏗️ 架构说明

### 多智能体工作流（Eino Graph）

系统使用 Eino Graph 编排多个 AI 智能体并行工作：

```
START → [市场分析师, 情绪分析师]（并行）
           ↓
市场分析师 → 加密货币分析师 → 持仓信息
           ↓                    ↓
       情绪分析师 ──────→ 交易员（综合决策）
                              ↓
                            END
```

### 市场报告格式

**日内报告**（基于 CRYPTO_TIMEFRAME，例如 3m）:
```
=== BTC Market Report ===

当前价格 = 95123.4, 当前 EMA(20) = 94567.2, 当前 MACD = 234.5, 当前 RSI(7) = 65.3

日内数据(3m)

中间价: [95100.0, 95150.0, 95200.0, ..., 95123.4]
EMA(20): [94500.0, 94520.0, 94540.0, ..., 94567.2]
MACD: [220.0, 225.0, 230.0, ..., 234.5]
RSI(7): [60.0, 62.0, 64.0, ..., 65.3]
RSI(14): [55.0, 56.0, 58.0, ..., 60.5]
```

**长期报告**（CRYPTO_LONGER_TIMEFRAME，例如 4h）:
```
长期数据 (4h):

EMA(20): 94567.2 vs. 50-Period EMA: 93500.0
ATR(3): 450.0 vs. 14-Period ATR: 520.0
当前成交量: 1250000.0 vs. 平均成交量: 1100000.0
MACD: [200.0, 210.0, 220.0, ..., 234.5]
RSI(14): [55.0, 56.0, 58.0, ..., 60.5]
```

---

## ⚙️ 常用命令

```bash
# 开发
make build        # 编译主程序
make build-all    # 编译所有组件
make test         # 运行测试
make test-cover   # 测试覆盖率
make fmt          # 格式化代码
make clean        # 清理编译产物

# 运行
make run          # 单次执行
make run-web      # Web 监控模式

# 查询
make query ARGS="stats"                 # 统计信息
make query ARGS="latest 5"              # 最近 5 次
make query ARGS="symbol BTC/USDT 3"     # 特定交易对
```

---

## ⚠️ 安全警告

**重要提示**：

1. **先测试模式**：先设置 `AUTO_EXECUTE=false`，观察 1-2 天
2. **小仓位开始**：从最小仓位和保守杠杆开始
3. **使用单向持仓**：`BINANCE_POSITION_MODE=oneway`（双向持仓模式有 bug）
4. **监控运行**：定期查看 Web 界面和日志
5. **API 安全**：
    - 使用 IP 白名单限制 API 访问
    - 永远不要分享你的 API 密钥
    - 只授予必要的权限（仅期货交易）
6. **动态杠杆**：使用 `10-20` 范围，LLM 根据置信度选择
7. **始终开启止损**：保持 `ENABLE_STOPLOSS=true`

**风险声明**：加密货币交易存在高风险，可能导致资金损失。本软件仅供学习和研究使用，使用者需自行承担所有风险。

---

## 🐛 故障排除

### 常见问题

1. **余额曲线图不显示**
    - 确保程序已运行至少 5-10 分钟
    - 检查数据库：`sqlite3 data/trading.db "SELECT COUNT(*) FROM balance_history;"`

2. **下次交易时间不正确**
    - 检查 `.env` 中的 `TRADING_INTERVAL` 是否正确设置
    - Web 页面现在会同时显示"K 线间隔"和"运行间隔"

3. **持仓显示异常**
    - 确认 `BINANCE_POSITION_MODE=oneway`（推荐）
    - 检查币安账户实际持仓模式

4. **编译错误**
    - 确保 Go 版本 >= 1.21
    - 运行 `make deps` 更新依赖
    - 清理后重新编译：`make clean && make build-all`

---

## 📚 更多文档

- [CLAUDE.md](CLAUDE.md) - 详细的项目指南和架构说明
- [prompts/README.md](prompts/README.md) - Prompt 管理和策略配置
- [.env.example](.env.example) - 完整的配置参数说明
- [docs/STOP_LOSS_GUIDE.md](docs/STOP_LOSS_GUIDE.md) - 止损管理指南

---

## 🔄 从 Python 版本迁移

本项目是从 Python 完全重写为 Go 版本：

**主要变化**：
- LangGraph → Eino Graph（Cloudwego）
- CCXT → go-binance（官方 SDK）
- pandas → 原生 Go 切片操作
- Flask → Hertz（Cloudwego）

**优势**：
- 更高的性能和并发能力
- 更低的资源占用
- 更快的启动速度
- 更好的类型安全

---

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

---

## 📄 许可证

[MIT License](LICENSE)

---

**⚡ Powered by Go + Cloudwego Eino + AI**

> 如有问题或建议，欢迎在 GitHub Issues 中反馈。
