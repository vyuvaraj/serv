//go:build !enterprise

package proxy

import (
	"fmt"
	"net/http"
)

func (h *GatewayHandler) checkAIBudgets(_ http.ResponseWriter, _ *http.Request, _ *Route, _ string) bool {
	// No-op in open-source
	return false
}

func (h *GatewayHandler) applyAIRequestExtensions(w http.ResponseWriter, r *http.Request, _ *Route, _ interface{}, reqBody []byte) ([]byte, bool) {
	// Estimate request prompt tokens: length of body / 4
	reqTokens := len(reqBody) / 4
	if reqTokens < 1 {
		reqTokens = 1
	}
	// Add estimated completion tokens (standard default 500)
	estTokens := reqTokens + 500
	estCost := float64(estTokens) * 0.000002
	if estCost < 0.003 {
		estCost = 0.003
	}

	w.Header().Set("X-Estimated-Cost", fmt.Sprintf("$%.3f", estCost))

	if r.Header.Get("X-Dry-Run") == "true" || r.Header.Get("X-Estimate-Only") == "true" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(fmt.Appendf(nil, `{"status":"dry-run","estimated_cost_usd":%.6f,"estimated_cost_str":"$%.3f"}`, estCost, estCost))
		return reqBody, false
	}

	return reqBody, true
}

func (h *GatewayHandler) applyAIResponseExtensions(_ *Route, _ *http.Request, _ *http.Response, _ []byte) {
	// No-op in open-source
}

func (h *GatewayHandler) chargeAITokens(_ *Route, _ *http.Request, _ *StatusRecordingResponseWriter, _ []byte) {
	// No-op in open-source
}
