//go:build !enterprise

package proxy

import (
	"net/http"
)

// Dummy structs for open-source build compilation
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type BudgetConfig struct {
	MaxTokensPerMinute int     `json:"max_tokens_per_minute"`
	MaxCostPerDay      float64 `json:"max_cost_per_day_usd"`
	AlertThreshold     float64 `json:"alert_threshold_percent"`
}

type AIBillingTracker struct{}

func NewAIBillingTracker() *AIBillingTracker {
	return &AIBillingTracker{}
}

func (h *GatewayHandler) handleLLMRouting(w http.ResponseWriter, r *http.Request, matchedRoute *Route, span interface{}) bool {
	if matchedRoute.LLMRouting == nil {
		return false
	}
	// LLM routing is an enterprise feature
	WriteJSONError(w, r, "Cost-Aware LLM Routing requires ServGate Enterprise Edition", "ERR_EE_REQUIRED", http.StatusForbidden)
	return true
}

func (h *GatewayHandler) GetAIBillingMetrics() map[string]interface{} {
	return map[string]interface{}{
		"error": "Enterprise Edition required for AI billing metrics",
	}
}

func (h *GatewayHandler) SetAIBudget(tenantID string, maxCostPerDay float64, maxTokensPerMinute int) {
	// No-op in open-source
}
