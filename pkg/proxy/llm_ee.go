//go:build enterprise

package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"servgate/pkg/otel"
)

func (h *GatewayHandler) handleLLMRouting(w http.ResponseWriter, r *http.Request, matchedRoute *Route, span interface{}) bool {
	if matchedRoute.LLMRouting == nil {
		return false
	}

	h.transportsMu.RLock()
	customTransport, hasTransport := h.transports[matchedRoute.Prefix]
	h.transportsMu.RUnlock()

	baseTransport := http.DefaultTransport
	if hasTransport {
		baseTransport = customTransport
	}

	llmConf := matchedRoute.LLMRouting
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body.Close()

	// 1. Primary request to cheaper model
	primaryURL, _ := url.Parse(llmConf.Primary.URL)
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, primaryURL.String(), bytes.NewReader(bodyBytes))
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if llmConf.Primary.Model != "" {
		req.Header.Set("X-LLM-Model", llmConf.Primary.Model)
	}

	client := &http.Client{Transport: baseTransport}
	resp, err := client.Do(req)

	var respBody []byte
	shouldFallback := false
	if err != nil {
		shouldFallback = true
	} else {
		respBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()

		if llmConf.ConfidenceHeader != "" {
			confStr := resp.Header.Get(llmConf.ConfidenceHeader)
			var confVal float64
			fmt.Sscanf(confStr, "%f", &confVal)
			if confVal < llmConf.MinConfidence {
				shouldFallback = true
			}
		}
		if resp.StatusCode >= 500 {
			shouldFallback = true
		}
	}

	if shouldFallback {
		// 2. Fallback request to premium model
		fallbackURL, _ := url.Parse(llmConf.Fallback.URL)
		reqFb, _ := http.NewRequestWithContext(r.Context(), r.Method, fallbackURL.String(), bytes.NewReader(bodyBytes))
		for k, vs := range r.Header {
			for _, v := range vs {
				reqFb.Header.Add(k, v)
			}
		}
		if llmConf.Fallback.Model != "" {
			reqFb.Header.Set("X-LLM-Model", llmConf.Fallback.Model)
		}
		respFb, errFb := client.Do(reqFb)
		if errFb != nil {
			WriteJSONError(w, r, "Bad Gateway: Fallback failed", "ERR_BAD_GATEWAY_LLM", http.StatusBadGateway)
			h.metricsTracker.IncError()
			return true
		}
		defer respFb.Body.Close()
		respBody, _ = io.ReadAll(respFb.Body)

		for k, vs := range respFb.Header {
			w.Header().Del(k)
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-LLM-Fallback", "true")
		w.WriteHeader(respFb.StatusCode)
		w.Write(respBody)

		otel.EndSpan(span.(*otel.Span), nil, map[string]interface{}{
			"http.route":         matchedRoute.Prefix,
			"llm.fallback":       true,
			"llm.fallback_model": llmConf.Fallback.Model,
		})
		return true
	}

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-LLM-Fallback", "false")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)

	otel.EndSpan(span.(*otel.Span), nil, map[string]interface{}{
		"http.route":        matchedRoute.Prefix,
		"llm.fallback":      false,
		"llm.primary_model": llmConf.Primary.Model,
	})
	return true
}

// GetAIBillingMetrics returns billing metrics for the admin API.
func (h *GatewayHandler) GetAIBillingMetrics() map[string]interface{} {
	return h.aiBilling.GetMetrics()
}

// SetAIBudget configures a spending limit for a tenant.
func (h *GatewayHandler) SetAIBudget(tenantID string, maxCostPerDay float64, maxTokensPerMinute int) {
	h.aiBilling.SetBudget(tenantID, &BudgetConfig{
		MaxCostPerDay:      maxCostPerDay,
		MaxTokensPerMinute: maxTokensPerMinute,
		AlertThreshold:     80.0,
	})
}
