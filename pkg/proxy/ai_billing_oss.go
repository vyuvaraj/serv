//go:build !enterprise

package proxy

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

// IsSemanticCacheSupported flags if the semantic cache is supported in this build.
const IsSemanticCacheSupported = false

func (h *GatewayHandler) getSemanticCache(_ *Route, _ string) ([]byte, bool) {
	// Semantic API Caching requires Enterprise Edition.
	return nil, false
}

func (h *GatewayHandler) setSemanticCache(_ *Route, _ string, _ []byte) {
	// No-op in OSS
}

func (h *GatewayHandler) handleLLMRouting(w http.ResponseWriter, r *http.Request, matchedRoute *Route, _ interface{}) bool {
	if matchedRoute.LLMRouting == nil {
		return false
	}
	// Cost-Aware LLM Routing requires Enterprise Edition.
	WriteJSONError(w, r, "Cost-Aware LLM Routing requires ServGate Enterprise Edition", "ERR_EE_REQUIRED", http.StatusForbidden)
	return true
}

func (h *GatewayHandler) proxyToLLMTarget(target LLMTarget, body []byte, originalReq *http.Request) ([]byte, http.Header, int, error) {
	req, err := http.NewRequest(originalReq.Method, target.URL+originalReq.URL.Path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}

	// Copy headers
	for k, vv := range originalReq.Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("X-LLM-Model", target.Model)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, 0, err
	}

	return respBytes, resp.Header, resp.StatusCode, nil
}

func (h *GatewayHandler) GetAIBillingMetrics() map[string]interface{} {
	return map[string]interface{}{
		"error": "Enterprise Edition required for AI billing metrics",
	}
}

func (h *GatewayHandler) SetAIBudget(tenantID string, maxCostPerDay float64, maxTokensPerMinute int) {
	// No-op in open-source
}
