package proxy

// TokenUsage tracks prompt and completion tokens.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// BudgetConfig defines the cost limits for AI usages.
type BudgetConfig struct {
	MaxTokensPerMinute int     `json:"max_tokens_per_minute"`
	MaxCostPerDay      float64 `json:"max_cost_per_day_usd"`
	AlertThreshold     float64 `json:"alert_threshold_percent"`
}

// AIBillingTracker tracks costs and tokens consumed by tenants.
type AIBillingTracker struct{}

// NewAIBillingTracker initializes the tracker.
func NewAIBillingTracker() *AIBillingTracker {
	return &AIBillingTracker{}
}

// GetMetrics returns billing metrics.
func (a *AIBillingTracker) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"total_cost_usd": 0.0,
	}
}

// SetBudget configures limits.
func (a *AIBillingTracker) SetBudget(tenantID string, cfg *BudgetConfig) {}
