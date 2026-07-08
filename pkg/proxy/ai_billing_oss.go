//go:build !enterprise

package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (h *GatewayHandler) handleLLMRouting(w http.ResponseWriter, r *http.Request, matchedRoute *Route, span interface{}) bool {
	if matchedRoute.LLMRouting == nil {
		return false
	}

	cfg := matchedRoute.LLMRouting

	// Read original request body so we can reuse/resend it if we fallback
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		WriteJSONError(w, r, "Failed to read request body", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return true
	}
	r.Body.Close()

	// 1. Try Primary target
	useFallback := false
	respBytes, respHeader, statusCode, err := h.proxyToLLMTarget(cfg.Primary, bodyBytes, r)

	if err != nil || statusCode >= 500 {
		useFallback = true
	} else if cfg.ConfidenceHeader != "" {
		confVal := respHeader.Get(cfg.ConfidenceHeader)
		if confVal != "" {
			if val, err := strconv.ParseFloat(confVal, 64); err == nil {
				if val < cfg.MinConfidence {
					useFallback = true
				}
			}
		}
	}

	var finalRespBytes []byte
	var finalHeader http.Header
	var finalStatusCode int
	isFallback := "false"

	if useFallback {
		// 2. Fallback to premium target
		isFallback = "true"
		fBytes, fHeader, fCode, fErr := h.proxyToLLMTarget(cfg.Fallback, bodyBytes, r)
		if fErr != nil {
			WriteJSONError(w, r, "Fallback target failed", "ERR_FALLBACK_FAILED", http.StatusBadGateway)
			return true
		}
		finalRespBytes = fBytes
		finalHeader = fHeader
		finalStatusCode = fCode
	} else {
		finalRespBytes = respBytes
		finalHeader = respHeader
		finalStatusCode = statusCode
	}

	// Copy response headers and body back
	for k, vv := range finalHeader {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-LLM-Fallback", isFallback)
	w.WriteHeader(finalStatusCode)
	w.Write(finalRespBytes)

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
