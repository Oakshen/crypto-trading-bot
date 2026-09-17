package agents

import (
	"strings"
	"testing"

	"github.com/oak/crypto-trading-bot/internal/executors"
)

// TestParseDecisionWithMarkdown tests parsing decisions with Markdown formatting
// TestParseDecisionWithMarkdown 测试解析带 Markdown 格式的决策
func TestParseDecisionWithMarkdown(t *testing.T) {
	tests := []struct {
		name           string
		decisionText   string
		expectedAction executors.TradeAction
		expectedValid  bool
		description    string
	}{
		{
			name: "Markdown formatted BUY decision",
			decisionText: `【SOL/USDT】
**交易方向**: BUY
**置信度**: 0.78
**杠杆倍数**: 12倍
**入场理由**: ADX 41.01显示强上升趋势`,
			expectedAction: executors.ActionBuy,
			expectedValid:  true,
			description:    "修复漏洞 #1-3: Markdown 星号 + 中文格式",
		},
		{
			name: "Uppercase action with lowercase pattern",
			decisionText: `交易方向: BUY
置信度: 0.85`,
			expectedAction: executors.ActionBuy,
			expectedValid:  true,
			description:    "修复漏洞 #1: 大小写不一致",
		},
		{
			name: "HOLD decision with Chinese",
			decisionText: `**交易方向**: HOLD
**置信度**: 0.65
**杠杆倍数**: 不适用`,
			expectedAction: executors.ActionHold,
			expectedValid:  true,
			description:    "HOLD 决策",
		},
		{
			name: "SELL decision with English",
			decisionText: `Action: SELL
Confidence: 0.80
Leverage: 15x`,
			expectedAction: executors.ActionSell,
			expectedValid:  true,
			description:    "英文格式",
		},
		{
			name: "CLOSE_LONG decision",
			decisionText: `决策方向: CLOSE_LONG
置信度: 0.75`,
			expectedAction: executors.ActionCloseLong,
			expectedValid:  true,
			description:    "平多仓",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ParseDecision(tt.decisionText, "BTC/USDT")

			if decision.Valid != tt.expectedValid {
				t.Errorf("%s: expected Valid=%v, got %v\nReason: %s",
					tt.description, tt.expectedValid, decision.Valid, decision.Reason)
			}

			if decision.Valid && decision.Action != tt.expectedAction {
				t.Errorf("%s: expected Action=%v, got %v",
					tt.description, tt.expectedAction, decision.Action)
			}
		})
	}
}

// TestExtractConfidence tests confidence extraction with various formats
// TestExtractConfidence 测试各种格式的置信度提取
func TestExtractConfidence(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		expected    float64
		description string
	}{
		{
			name:        "Decimal confidence",
			text:        "置信度: 0.78",
			expected:    0.78,
			description: "标准小数格式",
		},
		{
			name:        "Integer percentage",
			text:        "信心: 85%",
			expected:    0.85,
			description: "整数百分比（修复漏洞 #4）",
		},
		{
			name:        "Decimal percentage",
			text:        "信心: 78.5%",
			expected:    0.785,
			description: "小数百分比（修复漏洞 #4）",
		},
		{
			name:        "Markdown formatted",
			text:        "**置信度**: 0.92",
			expected:    0.92,
			description: "Markdown 格式（修复漏洞 #2）",
		},
		{
			name:        "English uppercase (lowercase in practice)",
			text:        strings.ToLower("CONFIDENCE: 0.65"), // extractConfidence receives lowercase text
			expected:    0.65,
			description: "英文大写（实际会被转换为小写）",
		},
		{
			name:        "No confidence specified",
			text:        "Action: BUY",
			expected:    0.7,
			description: "默认置信度",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractConfidence(tt.text)
			if result != tt.expected {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expected, result)
			}
		})
	}
}

// TestExtractLeverage tests leverage extraction with various formats
// TestExtractLeverage 测试各种格式的杠杆提取
func TestExtractLeverage(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		expected    int
		description string
	}{
		{
			name:        "Chinese format with 倍",
			text:        "杠杆倍数: 12倍",
			expected:    12,
			description: "中文格式（修复漏洞 #3）",
		},
		{
			name:        "Markdown formatted",
			text:        "**杠杆倍数**: 15倍",
			expected:    15,
			description: "Markdown 格式",
		},
		{
			name:        "English with x",
			text:        "Leverage: 20x",
			expected:    20,
			description: "英文格式（修复漏洞 #3）",
		},
		{
			name:        "Just number and x",
			text:        "15x",
			expected:    15,
			description: "简短格式",
		},
		{
			name:        "Number and 倍杠杆",
			text:        "10倍杠杆",
			expected:    10,
			description: "中文完整格式",
		},
		{
			name:        "No leverage specified",
			text:        "Action: BUY",
			expected:    0,
			description: "未指定杠杆",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractLeverage(tt.text)
			if result != tt.expected {
				t.Errorf("%s: expected %d, got %d", tt.description, tt.expected, result)
			}
		})
	}
}

// TestExtractStopLoss tests stop-loss extraction with various formats
// TestExtractStopLoss 测试各种格式的止损提取
func TestExtractStopLoss(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		expected    float64
		description string
	}{
		{
			name:        "Chinese with dollar sign",
			text:        "初始止损: $154.50",
			expected:    154.50,
			description: "中文 + 美元符号",
		},
		{
			name:        "Markdown formatted",
			text:        "**初始止损**: $154.50",
			expected:    154.50,
			description: "Markdown 格式（修复漏洞 #2）",
		},
		{
			name:        "With comma separator",
			text:        "止损: $1,234.56",
			expected:    1234.56,
			description: "千位分隔符",
		},
		{
			name:        "Without dollar sign",
			text:        "止损价: 100.25",
			expected:    100.25,
			description: "无美元符号",
		},
		{
			name:        "English format",
			text:        "stop-loss: $98.75",
			expected:    98.75,
			description: "英文格式",
		},
		{
			name:        "No stop-loss specified",
			text:        "Action: BUY",
			expected:    0,
			description: "未指定止损",
		},
		// Guards for the bare "止损" pattern: these must NOT be read as prices.
		// 裸写 "止损" 模式的防误匹配用例：以下内容不能被当成价格解析。
		{
			name:        "Stop-loss adjustment rationale",
			text:        "止损调整理由：价格已上涨 3.5%，上移止损",
			expected:    0,
			description: "止损调整理由不应被当作价格",
		},
		{
			name:        "Stop-loss rationale",
			text:        "止损理由：波动率放大",
			expected:    0,
			description: "止损理由不应被当作价格",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractStopLoss(tt.text)
			if result != tt.expected {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expected, result)
			}
		})
	}
}

// TestExtractReason tests reason extraction with various formats
// TestExtractReason 测试各种格式的理由提取
func TestExtractReason(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		expected    string
		description string
	}{
		{
			name:        "Chinese 入场理由",
			text:        "**入场理由**: ADX 41.01显示强上升趋势",
			expected:    "ADX 41.01显示强上升趋势",
			description: "修复漏洞 #6: 入场理由",
		},
		{
			name:        "Chinese 理由",
			text:        "理由: 趋势向上，MACD金叉",
			expected:    "趋势向上，MACD金叉",
			description: "中文理由",
		},
		{
			name:        "English uppercase REASON",
			text:        "REASON: Strong uptrend with high volume",
			expected:    "Strong uptrend with high volume",
			description: "英文大写（修复漏洞 #1）",
		},
		{
			name:        "Markdown formatted",
			text:        "**理由**: 市场情绪乐观",
			expected:    "市场情绪乐观",
			description: "Markdown 格式",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractReason(tt.text)
			if result != tt.expected {
				t.Errorf("%s:\nexpected: %q\ngot: %q", tt.description, tt.expected, result)
			}
		})
	}
}

// TestParseMultiCurrencyDecision tests parsing multi-currency decisions
// TestParseMultiCurrencyDecision 测试解析多币种决策
func TestParseMultiCurrencyDecision(t *testing.T) {
	decisionText := `【SOL/USDT】
**交易方向**: BUY
**置信度**: 0.78
**杠杆倍数**: 12倍
**入场理由**: ADX 41.01显示强上升趋势，价格突破布林带中轨且MACD转正
**初始止损**: $154.50

【BTC/USDT】
**交易方向**: HOLD
**置信度**: 0.65
**杠杆倍数**: 不适用
**入场理由**: ADX仅19.89显示无趋势，成交量萎缩

【ETH/USDT】
**交易方向**: HOLD
**置信度**: 0.60
**杠杆倍数**: 不适用`

	symbols := []string{"SOL/USDT", "BTC/USDT", "ETH/USDT"}
	decisions := ParseMultiCurrencyDecision(decisionText, symbols)

	// Test SOL/USDT
	if sol, ok := decisions["SOL/USDT"]; !ok {
		t.Error("SOL/USDT decision not found")
	} else {
		if sol.Action != executors.ActionBuy {
			t.Errorf("SOL/USDT: expected BUY, got %v", sol.Action)
		}
		if sol.Confidence != 0.78 {
			t.Errorf("SOL/USDT: expected confidence 0.78, got %v", sol.Confidence)
		}
		if sol.Leverage != 12 {
			t.Errorf("SOL/USDT: expected leverage 12, got %v", sol.Leverage)
		}
		if sol.StopLoss != 154.50 {
			t.Errorf("SOL/USDT: expected stop-loss 154.50, got %v", sol.StopLoss)
		}
	}

	// Test BTC/USDT
	if btc, ok := decisions["BTC/USDT"]; !ok {
		t.Error("BTC/USDT decision not found")
	} else {
		if btc.Action != executors.ActionHold {
			t.Errorf("BTC/USDT: expected HOLD, got %v", btc.Action)
		}
	}

	// Test ETH/USDT
	if eth, ok := decisions["ETH/USDT"]; !ok {
		t.Error("ETH/USDT decision not found")
	} else {
		if eth.Action != executors.ActionHold {
			t.Errorf("ETH/USDT: expected HOLD, got %v", eth.Action)
		}
	}
}

// TestParseMultiCurrencyDecision_JSON tests parsing JSON-based multi-currency decisions (map format)
// TestParseMultiCurrencyDecision_JSON 测试解析基于 JSON 的多币种决策（map 格式）
func TestParseMultiCurrencyDecision_JSON(t *testing.T) {
	jsonDecision := `{
  "BTC/USDT": {
    "symbol": "BTC/USDT",
    "action": "HOLD",
    "confidence": 0.88,
    "leverage": 25,
    "position_size": 2.57,
    "stop_loss": 92641.65,
    "reasoning": "趋势未破，MACD持续上行，但订单簿卖压显著需警惕回调",
    "risk_reward_ratio": 2.1,
    "summary": "当前多头持仓小幅浮亏，技术面仍呈上行结构，但订单簿卖盘主导。暂持有多仓，追踪止损保护，等待价格确认突破或反转信号。",
    "current_pnl_percent": -3.67
  },
  "SOL/USDT": {
    "symbol": "SOL/USDT",
    "action": "BUY",
    "confidence": 0.81,
    "leverage": 25,
    "position_size": 2.57,
    "stop_loss": 92641.65,
    "reasoning": "趋势未破，MACD持续上行，但订单簿卖压显著需警惕回调",
    "risk_reward_ratio": 2.1,
    "summary": "当前多头持仓小幅浮亏，技术面仍呈上行结构，但订单簿卖盘主导。暂持有多仓，追踪止损保护，等待价格确认突破或反转信号。",
    "current_pnl_percent": -3.67
  },
  "BNB/USDT": {
    "symbol": "BNB/USDT",
    "action": "BUY",
    "confidence": 0.81,
    "leverage": 25,
    "position_size": 2.57,
    "stop_loss": 92641.65,
    "reasoning": "趋势未破，MACD持续上行，但订单簿卖压显著需警惕回调",
    "risk_reward_ratio": 2.1,
    "summary": "当前多头持仓小幅浮亏，技术面仍呈上行结构，但订单簿卖盘主导。暂持有多仓，追踪止损保护，等待价格确认突破或反转信号。",
    "current_pnl_percent": -3.67
  },
  "ETH/USDT": {
    "symbol": "ETH/USDT",
    "action": "HOLD",
    "confidence": 0.88,
    "leverage": 12,
    "position_size": 8.0,
    "stop_loss": 3100.00,
    "reasoning": "趋势延续但未创新高",
    "risk_reward_ratio": 2.8,
    "summary": "空头趋势保持，价格在区间震荡未创新低，止损保持不变，继续观察",
    "current_pnl_percent": 5.2
  }
}`

	symbols := []string{"BTC/USDT", "SOL/USDT", "BNB/USDT", "ETH/USDT", "XRP/USDT"}
	decisions := ParseMultiCurrencyDecision(jsonDecision, symbols)

	// BTC/USDT: HOLD
	if btc, ok := decisions["BTC/USDT"]; !ok {
		t.Error("BTC/USDT decision not found")
	} else {
		if btc.Action != executors.ActionHold {
			t.Errorf("BTC/USDT: expected HOLD, got %v", btc.Action)
		}
		if !btc.Valid {
			t.Errorf("BTC/USDT decision should be valid")
		}
	}

	// SOL/USDT: BUY
	if sol, ok := decisions["SOL/USDT"]; !ok {
		t.Error("SOL/USDT decision not found")
	} else {
		if sol.Action != executors.ActionBuy {
			t.Errorf("SOL/USDT: expected BUY, got %v", sol.Action)
		}
		if sol.PositionSizePercent <= 0 {
			t.Errorf("SOL/USDT: expected positive position size, got %v", sol.PositionSizePercent)
		}
	}

	// BNB/USDT: BUY
	if bnb, ok := decisions["BNB/USDT"]; !ok {
		t.Error("BNB/USDT decision not found")
	} else {
		if bnb.Action != executors.ActionBuy {
			t.Errorf("BNB/USDT: expected BUY, got %v", bnb.Action)
		}
	}

	// ETH/USDT: HOLD
	if eth, ok := decisions["ETH/USDT"]; !ok {
		t.Error("ETH/USDT decision not found")
	} else {
		if eth.Action != executors.ActionHold {
			t.Errorf("ETH/USDT: expected HOLD, got %v", eth.Action)
		}
	}

	// XRP/USDT: not present in JSON, should default to HOLD
	if xrp, ok := decisions["XRP/USDT"]; !ok {
		t.Error("XRP/USDT decision not found")
	} else {
		if xrp.Action != executors.ActionHold {
			t.Errorf("XRP/USDT: expected default HOLD, got %v", xrp.Action)
		}
		if !xrp.Valid {
			t.Errorf("XRP/USDT default decision should be valid")
		}
	}
}

// TestRealWorldDecision tests with actual LLM output from the user's issue
// TestRealWorldDecision 使用用户实际问题中的 LLM 输出进行测试
func TestRealWorldDecision(t *testing.T) {
	// This is the exact format from the user's issue
	// 这是用户问题中的确切格式
	realDecision := `【SOL/USDT】
**交易方向**: BUY
**置信度**: 0.78
**杠杆倍数**: 12倍
**入场理由**: ADX 41.01显示强上升趋势，价格突破布林带中轨且MACD转正，订单簿买单量显著大于卖单量
**初始止损**: $154.50（基于布林带下轨支撑）
**预期盈亏比**: 2.5:1（止损$1.43 vs 目标$3.6+）
**仓位建议**: 30%资金`

	decision := ParseDecision(realDecision, "SOL/USDT")

	if !decision.Valid {
		t.Fatalf("Decision should be valid. Reason: %s", decision.Reason)
	}

	if decision.Action != executors.ActionBuy {
		t.Errorf("Expected BUY action, got %v", decision.Action)
	}

	if decision.Confidence != 0.78 {
		t.Errorf("Expected confidence 0.78, got %v", decision.Confidence)
	}

	if decision.Leverage != 12 {
		t.Errorf("Expected leverage 12, got %v", decision.Leverage)
	}

	if decision.StopLoss != 154.50 {
		t.Errorf("Expected stop-loss 154.50, got %v", decision.StopLoss)
	}

	t.Logf("✅ Successfully parsed real-world decision:")
	t.Logf("   Action: %v", decision.Action)
	t.Logf("   Confidence: %v", decision.Confidence)
	t.Logf("   Leverage: %v", decision.Leverage)
	t.Logf("   Stop-Loss: %v", decision.StopLoss)
	t.Logf("   Reason: %v", decision.Reason)
}
