//go:build !enterprise

package proxy

import "net/http"

// RunAIWAF is the OSS stub for the Self-Defending AI WAF (AI.10).
// In OSS builds this is a no-op; the Enterprise Edition overlay provides
// semantic abuse scoring, anomaly detection and adaptive blocking.
func RunAIWAF(_ *http.Request, _ string, _ []byte) (blocked bool, reason string) {
	return false, ""
}
