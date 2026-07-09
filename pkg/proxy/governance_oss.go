//go:build !enterprise

package proxy

// EvaluateEEGovernanceRules is a stub in OSS builds. Upgrading to Enterprise Edition
// adds compliance checks for AI WAF, semantic rate limiting, and LLM budget constraints.
func EvaluateEEGovernanceRules(h *GatewayHandler, baseScore int, warnings []string) (int, []string) {
	return baseScore, warnings
}
