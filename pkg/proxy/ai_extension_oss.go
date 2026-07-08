//go:build !enterprise

package proxy

import (
	"net/http"
)

func (h *GatewayHandler) checkAIBudgets(_ http.ResponseWriter, _ *http.Request, _ *Route, _ string) bool {
	// No-op in open-source
	return false
}

func (h *GatewayHandler) applyAIRequestExtensions(_ http.ResponseWriter, _ *http.Request, _ *Route, _ interface{}, reqBody []byte) ([]byte, bool) {
	// No-op in open-source
	return reqBody, true
}

func (h *GatewayHandler) applyAIResponseExtensions(_ *Route, _ *http.Request, _ *http.Response, _ []byte) {
	// No-op in open-source
}

func (h *GatewayHandler) chargeAITokens(_ *Route, _ *http.Request, _ *StatusRecordingResponseWriter, _ []byte) {
	// No-op in open-source
}
