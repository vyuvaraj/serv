//go:build !enterprise

package proxy

import (
	"net/http"
)

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
