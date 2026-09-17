package agents

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/oak/crypto-trading-bot/internal/executors"
)

// todo 指定大模型以 json 模式输出 或者 用小模型读取大模型输出，然后结构化输出 json

// TradingDecision represents a parsed trading decision from LLM
// TradingDecision 表示从 LLM 解析出的交易决策
type TradingDecision struct {
	Action              executors.TradeAction // 交易动作 / Trading action
	Confidence          float64               // 置信度 0-1 / Confidence level 0-1
	Leverage            int                   // 杠杆倍数 / Leverage multiplier
	Reason              string                // 决策理由 / Decision reason
	Symbol              string                // 交易对 / Trading pair
	StopLoss            float64               // 止损价格 / Stop-loss price
	PositionSizePercent float64               // 仓位百分比 0-100 / Position size percentage (e.g., 40 = 40%)
	Valid               bool                  // 决策是否有效 / Whether decision is valid
}

// ParseDecision parses LLM decision text and extracts trading action
// ParseDecision 解析 LLM 决策文本并提取交易动作
func ParseDecision(decisionText string, symbol string) *TradingDecision {
	decision := &TradingDecision{
		Symbol: symbol,
		Valid:  false,
	}

	// Convert to lowercase for case-insensitive matching
	// 转换为小写以进行不区分大小写的匹配
	text := strings.ToLower(decisionText)

	// Extract action using multiple patterns
	// 使用多种模式提取交易动作
	action := extractAction(text)
	if action == "" {
		decision.Reason = "无法从决策文本中识别明确的交易动作"
		return decision
	}

	// Map action string to TradeAction enum
	// 将动作字符串映射到 TradeAction 枚举
	decision.Action = mapToTradeAction(action)
	if decision.Action == "" {
		decision.Reason = fmt.Sprintf("未知的交易动作: %s", action)
		return decision
	}

	// Extract confidence (optional)
	// 提取置信度（可选）
	decision.Confidence = extractConfidence(text)

	// Extract leverage (optional)
	// 提取杠杆倍数（可选）
	decision.Leverage = extractLeverage(text)

	// Extract stop-loss price (NEW!)
	// 提取止损价格（新功能）
	decision.StopLoss = extractStopLoss(text)

	// Extract position size percentage (NEW!)
	// 提取仓位百分比（新功能）
	decision.PositionSizePercent = extractPositionSizePercent(text)

	// Extract reason (pass lowercase text for consistency)
	// 提取理由（传入小写文本以保持一致性）
	decision.Reason = extractReason(text)

	// Mark as valid
	// 标记为有效
	decision.Valid = true

	return decision
}

// extractAction extracts trading action from text using regex patterns
// extractAction 使用正则表达式从文本中提取交易动作
func extractAction(text string) string {
	// First try to extract from decision markers (highest priority)
	// 首先尝试从决策标记中提取（最高优先级）
	// Supports Markdown formatting like **方向**: BUY or **交易方向**: BUY
	// 支持 Markdown 格式如 **方向**: BUY 或 **交易方向**: BUY
	decisionPatterns := []string{
		`\*{0,2}(?:最终决策|决策方向|交易方向|方向)\*{0,2}[：:\s]*([a-z_]+)`,         // **方向**: buy or **交易方向**: buy
		`\*{0,2}(?:decision|action|direction)\*{0,2}[：:\s]*([a-z_]+)`, // **direction**: sell
	}

	for _, pattern := range decisionPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			action := strings.TrimSpace(matches[1]) // Already lowercase, no need to convert again
			return action
		}
	}

	// Patterns for different actions
	// 不同动作的匹配模式
	patterns := map[string][]string{
		"buy": {
			`建议.*?做多`,
			`建议.*?买入`,
			`建议.*?开多`,
			`action.*?buy`,
			`recommend.*?buy`,
			`decision.*?buy`,
			`做多`,
			`开多仓`,
			`买入`,
		},
		"sell": {
			`建议.*?做空`,
			`建议.*?卖出`,
			`建议.*?开空`,
			`action.*?sell`,
			`recommend.*?sell`,
			`decision.*?sell`,
			`做空`,
			`开空仓`,
			`卖出`,
		},
		"close_long": {
			`建议.*?平多`,
			`建议.*?平掉多单`,
			`close.*?long`,
			`平多仓`,
			`平掉多头`,
		},
		"close_short": {
			`建议.*?平空`,
			`建议.*?平掉空单`,
			`close.*?short`,
			`平空仓`,
			`平掉空头`,
		},
		"hold": {
			`建议.*?观望`,
			`建议.*?持有`,
			`建议.*?等待`,
			`action.*?hold`,
			`recommend.*?hold`,
			`decision.*?hold`,
			`观望`,
			`持有`,
			`不建议操作`,
		},
	}

	// Try each pattern
	// 尝试每个模式
	for action, patternList := range patterns {
		for _, pattern := range patternList {
			matched, _ := regexp.MatchString(pattern, text)
			if matched {
				return action
			}
		}
	}

	return ""
}

// mapToTradeAction maps action string to TradeAction enum
// mapToTradeAction 将动作字符串映射到 TradeAction 枚举
func mapToTradeAction(action string) executors.TradeAction {
	switch action {
	case "buy":
		return executors.ActionBuy
	case "sell":
		return executors.ActionSell
	case "close_long":
		return executors.ActionCloseLong
	case "close_short":
		return executors.ActionCloseShort
	case "hold":
		return executors.ActionHold
	default:
		return ""
	}
}

// extractConfidence extracts confidence level from text
// extractConfidence 从文本中提取置信度
func extractConfidence(text string) float64 {
	// Look for confidence patterns like "置信度: 0.8" or "confidence: 80%" or "信心: 78.5%"
	// 查找置信度模式，如 "置信度: 0.8" 或 "confidence: 80%" 或 "信心: 78.5%"
	patterns := []string{
		`\*{0,2}置信度\*{0,2}[：:\s]*([0-9.]+)`,        // 置信度: 0.78 or **置信度**: 0.78
		`\*{0,2}confidence\*{0,2}[：:\s]*([0-9.]+)`, // confidence: 0.8 or **confidence**: 0.8
		`\*{0,2}信心\*{0,2}[：:\s]*([0-9.]+)%?`,       // 信心: 78% or 信心: 78.5% or **信心**: 78%
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			var conf float64
			fmt.Sscanf(matches[1], "%f", &conf)
			if conf > 1 {
				conf = conf / 100.0 // Convert percentage to decimal / 将百分比转换为小数
			}
			return conf
		}
	}

	// Default confidence
	// 默认置信度
	return 0.7
}

// extractLeverage extracts leverage multiplier from text
// extractLeverage 从文本中提取杠杆倍数
func extractLeverage(text string) int {
	// Look for leverage patterns like "杠杆倍数: 15" or "leverage: 15x" or "12倍"
	// 查找杠杆模式，如 "杠杆倍数: 15" 或 "leverage: 15x" 或 "12倍"
	// ⚠️ 顺序很重要！更具体的模式必须在更宽泛的模式之前
	patterns := []string{
		`杠杆倍数\*{0,2}[：:\s]*\*{0,2}\s*([0-9]+)[x倍]?`,  // **杠杆倍数**: 15x or 15倍 or 杠杆倍数: 15
		`杠杆\*{0,2}[：:\s]*\*{0,2}\s*([0-9]+)[x倍]?`,    // **杠杆**: 10x or 10倍 or 杠杆: 10
		`leverage\*{0,2}[：:\s]*\*{0,2}\s*([0-9]+)x?`, // **leverage**: 15x or leverage: 15
		`([0-9]+)x(?:\s*leverage)?`,                  // 15x or 15x leverage (放在"倍"之前)
		`([0-9]+)倍杠杆`,                                // 12倍杠杆 (必须有"杠杆"二字，避免误匹配)
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			var leverage int
			fmt.Sscanf(matches[1], "%d", &leverage)
			if leverage >= 1 && leverage <= 125 {
				return leverage
			}
		}
	}

	// If no leverage found, return 0 (will use config default)
	// 如果未找到杠杆，返回 0（将使用配置默认值）
	return 0
}

// extractStopLoss extracts stop-loss price from text
// extractStopLoss 从文本中提取止损价格
func extractStopLoss(text string) float64 {
	// Look for stop-loss patterns (supports Markdown formatting like ** and various price formats)
	// 查找止损模式（支持 Markdown 格式如 ** 和各种价格格式）
	// ⚠️  Order matters! More specific patterns must come first
	// ⚠️  顺序很重要！更具体的模式必须放在前面
	patterns := []string{
		`\*{0,2}止损价格\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`,                   // **止损价格**: $3179.77 (最具体，优先匹配)
		`\*{0,2}止损价\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`,                    // **止损价**: 154.50
		`\*{0,2}止损点\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`,                    // **止损点**: 154.50
		`\*{0,2}初始止损\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`,                   // **初始止损**: $154.50
		`\*{0,2}stop[-\s]?loss\s*price\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`, // stop-loss price: $100
		`\*{0,2}stop[-\s]?loss\*{0,2}[：:\s]*\$?\s*([0-9,.]+)`,         // stop-loss: $100
		// Bare "止损" is only matched when a colon follows it immediately, which
		// is what keeps "止损调整理由：..." and "止损理由：..." from matching:
		// there the colon comes after the extra words, not after 止损.
		// 只有当冒号紧跟在 "止损" 之后时才匹配裸写的 "止损"，
		// 这正是 "止损调整理由：..."、"止损理由：..." 不会被匹配的原因：
		// 那些情况下冒号出现在附加词之后，而不是紧跟 "止损"。
		`\*{0,2}止损\*{0,2}[：:]\s*\$?\s*([0-9,.]+)`, // 止损: $1,234.56
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			// Clean the matched string (remove commas, dollar signs, and brackets)
			// 清理匹配的字符串（移除逗号、美元符号和方括号）
			priceStr := strings.ReplaceAll(matches[1], ",", "")
			priceStr = strings.ReplaceAll(priceStr, "$", "")
			// Extract number before brackets if present (e.g., "3179.77[保持]" -> "3179.77")
			// 如果存在方括号，提取括号前的数字（例如 "3179.77[保持]" -> "3179.77"）
			if idx := strings.Index(priceStr, "["); idx > 0 {
				priceStr = priceStr[:idx]
			}
			priceStr = strings.TrimSpace(priceStr)

			var stopLoss float64
			if _, err := fmt.Sscanf(priceStr, "%f", &stopLoss); err == nil && stopLoss > 0 {
				return stopLoss
			}
		}
	}

	// If no explicit stop-loss found, return 0 (will be calculated programmatically)
	// 如果未找到明确的止损价格，返回 0（将由程序计算）
	return 0
}

// extractReason extracts the decision reason from text
// extractReason 从文本中提取决策理由
func extractReason(text string) string {
	// Look for reason patterns (now using case-insensitive matching)
	// 查找理由模式（现在使用不区分大小写匹配）
	patterns := []string{
		`(?i)\*{0,2}理由\*{0,2}[：:\s]*(.+?)(?:\n|$)`,     // 理由: xxx or **理由**: xxx
		`(?i)\*{0,2}原因\*{0,2}[：:\s]*(.+?)(?:\n|$)`,     // 原因: xxx
		`(?i)\*{0,2}入场理由\*{0,2}[：:\s]*(.+?)(?:\n|$)`,   // **入场理由**: xxx
		`(?i)\*{0,2}reason\*{0,2}[：:\s]*(.+?)(?:\n|$)`, // reason: xxx or REASON: xxx
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			reason := strings.TrimSpace(matches[1])
			// Remove trailing Markdown symbols
			// 移除末尾的 Markdown 符号
			reason = strings.TrimRight(reason, "*")
			return reason
		}
	}

	// If no specific reason pattern, try to extract first meaningful sentence
	// 如果没有特定的理由模式，尝试提取第一个有意义的句子
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip headers (starting with #) and very short lines
		// 跳过标题（以 # 开头）和很短的行
		if len(line) > 30 && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "**交易方向") {
			// Remove Markdown formatting
			// 移除 Markdown 格式
			line = strings.ReplaceAll(line, "**", "")
			line = strings.TrimSpace(line)
			return line
		}
	}

	return "未提供明确理由"
}

// ValidateDecision performs safety checks on the decision
// ValidateDecision 对决策执行安全检查
func ValidateDecision(decision *TradingDecision, currentPosition *executors.Position) error {
	if !decision.Valid {
		return fmt.Errorf("无效的决策")
	}

	// Check for conflicting actions
	// 检查冲突的动作
	if currentPosition != nil {
		switch decision.Action {
		case executors.ActionBuy:
			if currentPosition.Side == "long" {
				return fmt.Errorf("已有多仓，不能重复开多")
			}
		case executors.ActionSell:
			if currentPosition.Side == "short" {
				return fmt.Errorf("已有空仓，不能重复开空")
			}
		case executors.ActionCloseLong:
			if currentPosition.Side != "long" {
				return fmt.Errorf("没有多仓可平")
			}
		case executors.ActionCloseShort:
			if currentPosition.Side != "short" {
				return fmt.Errorf("没有空仓可平")
			}
		}
	}

	return nil
}

// ParseMultiCurrencyDecision parses multi-currency decision text and extracts trading actions for each symbol
// ParseMultiCurrencyDecision 解析多币种决策文本并为每个交易对提取交易动作
func ParseMultiCurrencyDecision(decisionText string, symbols []string) map[string]*TradingDecision {
	decisions := make(map[string]*TradingDecision)

	trimmed := strings.TrimSpace(decisionText)

	// First, try to parse structured JSON decisions (multi-symbol or single-symbol)
	// 首先尝试解析结构化 JSON 决策（多币种或单币种）
	if strings.HasPrefix(trimmed, "{") {
		if jsonDecisions := parseJSONMultiCurrencyDecision(trimmed, symbols); jsonDecisions != nil {
			return jsonDecisions
		}
	}

	// Extract only the "最终决策" section to avoid parsing analysis text
	// 只提取"最终决策"部分以避免解析分析文本
	finalDecisionSection := extractFinalDecisionSection(decisionText)

	// Try to find decision blocks for each symbol
	// 尝试为每个交易对找到决策块
	for _, symbol := range symbols {
		// Create patterns for this symbol (e.g., "btc/usdt", "btc", "【btc/usdt】")
		// 为该交易对创建模式
		symbolLower := strings.ToLower(symbol)
		baseSymbol := strings.Split(symbolLower, "/")[0] // e.g., "btc" from "btc/usdt"

		// Find the decision block for this symbol
		// 查找该交易对的决策块
		// Use case-insensitive regex for matching symbol headers
		// (?s) makes . match newlines, (?i) makes matching case-insensitive
		// 使用不区分大小写的正则表达式匹配交易对标题
		// (?s) 让 . 匹配换行符，(?i) 让匹配不区分大小写
		patterns := []string{
			fmt.Sprintf(`(?si)【%s】(.{0,1000}?)(?:【|$)`, symbolLower),
			fmt.Sprintf(`(?si)【%s】(.{0,1000}?)(?:【|$)`, baseSymbol),
			fmt.Sprintf(`(?si)\*{0,2}%s\*{0,2}(.{0,1000}?)(?:\n\n|$)`, symbolLower), // Match **BTC/USDT** or BTC/USDT
		}

		var blockText string
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			// Search in final decision section only, not in analysis section
			// 只在最终决策部分搜索，不在分析部分搜索
			matches := re.FindStringSubmatch(finalDecisionSection)
			if len(matches) > 1 {
				blockText = matches[1]
				break
			}
		}

		// If we found a block, parse it
		// 如果找到了决策块，解析它
		if blockText != "" {
			decision := ParseDecision(blockText, symbol)
			decisions[symbol] = decision
		} else {
			// No specific block found, default to HOLD
			// 未找到特定决策块，默认为 HOLD
			decisions[symbol] = &TradingDecision{
				Symbol:     symbol,
				Action:     executors.ActionHold,
				Confidence: 0.5,
				Reason:     "未在决策中明确提及，默认观望",
				Valid:      true,
			}
		}
	}

	return decisions
}

// parseJSONMultiCurrencyDecision parses JSON-based decisions for multiple symbols
// parseJSONMultiCurrencyDecision 解析基于 JSON 的多币种决策
func parseJSONMultiCurrencyDecision(jsonText string, symbols []string) map[string]*TradingDecision {
	decisions := make(map[string]*TradingDecision)

	// Try to parse as map[string]TradeDecision (multi-symbol format, e.g. test.json)
	// 尝试解析为 map[string]TradeDecision（多币种格式，例如 test.json）
	var multi map[string]TradeDecision
	if err := json.Unmarshal([]byte(jsonText), &multi); err == nil && len(multi) > 0 {
		for _, symbol := range symbols {
			if td, ok := multi[symbol]; ok {
				decisions[symbol] = convertTradeDecisionToTradingDecision(&td)
			} else {
				// If symbol not present in JSON, default to HOLD
				// 如果 JSON 中没有该交易对，默认观望
				decisions[symbol] = &TradingDecision{
					Symbol:     symbol,
					Action:     executors.ActionHold,
					Confidence: 0.5,
					Reason:     "JSON 中未提供该交易对决策，默认观望",
					Valid:      true,
				}
			}
		}
		return decisions
	}

	// Try to parse as single TradeDecision object
	// 尝试解析为单个 TradeDecision 对象
	var single TradeDecision
	if err := json.Unmarshal([]byte(jsonText), &single); err == nil && single.Symbol != "" {
		singleDecision := convertTradeDecisionToTradingDecision(&single)
		for _, symbol := range symbols {
			if symbol == single.Symbol {
				decisions[symbol] = singleDecision
			} else {
				// Symbols not mentioned default to HOLD
				// 未被提及的交易对默认观望
				decisions[symbol] = &TradingDecision{
					Symbol:     symbol,
					Action:     executors.ActionHold,
					Confidence: 0.5,
					Reason:     "JSON 中未提及该交易对，默认观望",
					Valid:      true,
				}
			}
		}
		return decisions
	}

	// JSON parsing failed, fall back to text-based parsing
	// JSON 解析失败，回退到基于文本的解析
	return nil
}

// convertTradeDecisionToTradingDecision converts JSON TradeDecision into internal TradingDecision
// convertTradeDecisionToTradingDecision 将 JSON TradeDecision 转换为内部 TradingDecision
func convertTradeDecisionToTradingDecision(td *TradeDecision) *TradingDecision {
	if td == nil {
		return &TradingDecision{
			Valid:  false,
			Reason: "空的 JSON 决策对象",
		}
	}

	actionStr := strings.ToLower(strings.TrimSpace(td.Action))
	tradeAction := mapToTradeAction(actionStr)

	// Determine stop-loss to use
	// 决定应使用的止损价格
	stopLoss := td.StopLoss
	// For HOLD, prefer new_stop_loss if provided
	// 对于 HOLD，如提供了 new_stop_loss，则优先使用
	if strings.EqualFold(td.Action, "HOLD") && td.NewStopLoss != nil && *td.NewStopLoss > 0 {
		stopLoss = *td.NewStopLoss
	}

	// Build reason string, prefer stop-loss specific reason if available
	// 构建理由字符串，如有止损调整理由则优先使用
	reason := strings.TrimSpace(td.Reasoning)
	if td.StopLossReason != nil && strings.TrimSpace(*td.StopLossReason) != "" {
		reason = strings.TrimSpace(*td.StopLossReason)
	}
	if reason == "" {
		reason = strings.TrimSpace(td.Summary)
	}

	decision := &TradingDecision{
		Symbol:              td.Symbol,
		Action:              tradeAction,
		Confidence:          td.Confidence,
		Leverage:            td.Leverage,
		Reason:              reason,
		StopLoss:            stopLoss,
		PositionSizePercent: td.PositionSize,
		Valid:               true,
	}

	// If action is unknown, mark as invalid but keep parsed context
	// 如果动作未知，则标记为无效，但保留已解析的上下文信息
	if tradeAction == "" {
		decision.Valid = false
		decision.Reason = fmt.Sprintf("未知的 JSON 交易动作: %s", td.Action)
	}

	return decision
}

// extractFinalDecisionSection extracts only the final decision section from LLM output
// extractFinalDecisionSection 从 LLM 输出中只提取最终决策部分
func extractFinalDecisionSection(text string) string {
	// Look for section headers that indicate final decisions
	// 查找表示最终决策的章节标题
	patterns := []string{
		`(?si)##\s*最终决策[：:\s]*(.*)`,             // ## 最终决策：
		`(?si)##\s*交易决策[：:\s]*(.*)`,             // ## 交易决策：
		`(?si)##\s*final\s*decision[：:\s]*(.*)`, // ## Final Decision:
		`(?si)##\s*决策[：:\s]*(.*)`,               // ## 决策：
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			return matches[1] // Return everything after the header
		}
	}

	// If no section found, return full text (backward compatibility)
	// 如果未找到章节，返回完整文本（向后兼容）
	return text
}

// extractPositionSizePercent extracts position size percentage from text
// extractPositionSizePercent 从文本中提取仓位百分比
func extractPositionSizePercent(text string) float64 {
	// Look for position size patterns like "仓位建议: 40%资金" or "position: 30%"
	// 查找仓位模式，如 "仓位建议: 40%资金" 或 "position: 30%"
	patterns := []string{
		`\*{0,2}仓位建议\*{0,2}[：:\s]*([0-9.]+)%`,            // 仓位建议: 40% or **仓位建议**: 40%资金
		`\*{0,2}建议仓位\*{0,2}[：:\s]*([0-9.]+)%`,            // 建议仓位: 30%
		`\*{0,2}position\s*size\*{0,2}[：:\s]*([0-9.]+)%`, // position size: 25%
		`使用\s*([0-9.]+)%\s*(?:的)?资金`,                     // 使用 40% 资金 or 使用 40% 的资金
		`([0-9.]+)%\s*资金`,                                // 40%资金
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			var percent float64
			if _, err := fmt.Sscanf(matches[1], "%f", &percent); err == nil {
				// Validate range (0-100)
				// 验证范围（0-100）
				if percent > 0 && percent <= 100 {
					return percent
				}
			}
		}
	}

	// If no position size found, return 0 (will use config default)
	// 如果未找到仓位建议，返回 0（将使用配置默认值）
	return 0
}

// ValidateLeverage validates and returns the appropriate leverage to use
// ValidateLeverage 验证并返回应使用的杠杆倍数
func ValidateLeverage(llmLeverage int, minLeverage int, maxLeverage int, dynamic bool) int {
	// If dynamic leverage is disabled, use min leverage (which equals max in fixed mode)
	// 如果未启用动态杠杆，使用最小杠杆（固定模式下最小值等于最大值）
	if !dynamic {
		return minLeverage
	}

	// If LLM didn't specify leverage, use minimum for safety
	// 如果 LLM 未指定杠杆，为安全起见使用最小值
	if llmLeverage == 0 {
		return minLeverage
	}

	// Validate LLM leverage is within range
	// 验证 LLM 杠杆在范围内
	if llmLeverage < minLeverage {
		return minLeverage
	}
	if llmLeverage > maxLeverage {
		return maxLeverage
	}

	// Use LLM's choice
	// 使用 LLM 的选择
	return llmLeverage
}
