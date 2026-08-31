package piagent

import (
	"math"
	"strconv"
	"strings"
	"time"

	"PromAI/pkg/database"
)

// estimateTokens 粗略估算 token 数：按「字节数 / 2.5」保守估算（中文约 1 token/字、英文约 4 字符/token 的折中）。
// 仅用于模型未返回 usage 时的兜底，结果会标记 estimated。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / 2.5))
}

// loadTokenPrice 从 AppSetting 读取模型单价表（key: ai_price_<modelName>）。
// 值格式 "输入价,输出价"，单位为 元/百万 token。未配置时返回 ok=false。
func loadTokenPrice(modelName string) (inputPrice, outputPrice float64, ok bool) {
	if database.DB == nil || strings.TrimSpace(modelName) == "" {
		return 0, 0, false
	}
	var setting database.AppSetting
	if err := database.DB.Where("key = ?", "ai_price_"+strings.TrimSpace(modelName)).First(&setting).Error; err != nil {
		return 0, 0, false
	}
	parts := strings.Split(setting.Value, ",")
	if len(parts) < 2 {
		return 0, 0, false
	}
	in, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	out, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return in, out, true
}

// estimateCost 依据单价表估算一次分析的成本（元）。未配置单价时返回 nil（只展示 token 不折算金额）。
func estimateCost(modelName string, promptTokens, completionTokens int) *float64 {
	in, out, ok := loadTokenPrice(modelName)
	if !ok {
		return nil
	}
	cost := (in*float64(promptTokens) + out*float64(completionTokens)) / 1_000_000
	return &cost
}

// dailyTokenBudget 读取日 token 预算（key: ai_daily_token_budget，默认 500000，0=不限）。
func dailyTokenBudget() int {
	const fallback = 500_000
	if database.DB == nil {
		return fallback
	}
	var setting database.AppSetting
	if err := database.DB.Where("key = ?", "ai_daily_token_budget").First(&setting).Error; err != nil {
		return fallback
	}
	v, err := strconv.Atoi(strings.TrimSpace(setting.Value))
	if err != nil {
		return fallback
	}
	return v
}

// dailyTokenUsed 统计今日（本地时区 00:00 起）已消耗的总 token 数。
func dailyTokenUsed() int64 {
	if database.DB == nil {
		return 0
	}
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var sum int64
	_ = database.DB.Model(&database.AiAnalysisRecord{}).
		Where("created_at >= ?", startOfDay).
		Select("COALESCE(SUM(total_tokens), 0)").
		Scan(&sum)
	return sum
}

// TokenBudgetExceeded 判断日预算是否耗尽（0=不限时永不超）。
func TokenBudgetExceeded() bool {
	budget := dailyTokenBudget()
	if budget <= 0 {
		return false
	}
	return dailyTokenUsed() >= int64(budget)
}
