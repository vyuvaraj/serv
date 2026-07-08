//go:build !enterprise

package proxy

import (
	"net/http"
)

func (h *GatewayHandler) checkAIBudgets(w http.ResponseWriter, r *http.Request, matchedRoute *Route, apiKeyVal string) bool {
	// No-op in open-source
	return false
}

func (h *GatewayHandler) applyAIRequestExtensions(w http.ResponseWriter, r *http.Request, matchedRoute *Route, span interface{}, reqBody []byte) ([]byte, bool) {
	// No-op in open-source
	return reqBody, true
}

func (h *GatewayHandler) applyAIResponseExtensions(matchedRoute *Route, r *http.Request, resp *http.Response, bodyBytes []byte) {
	// No-op in open-source
}

func (h *GatewayHandler) chargeAITokens(matchedRoute *Route, r *http.Request, rec *StatusRecordingResponseWriter, reqBody []byte) {
	// No-op in open-source
}
