//go:build !enterprise

package s3

import "net/http"

// resolveFederatedBucket in OSS: federation disabled, always returns false.
func (g *Gateway) resolveFederatedBucket(bucket string) (string, bool) {
	return "", false
}

// handleRegisterFederation in OSS: returns 403.
func (g *Gateway) handleRegisterFederation(w http.ResponseWriter, r *http.Request) {
	g.writeErrorCtx(w, r, http.StatusForbidden, "EnterpriseRequired", "Cross-cluster federation requires ServStore Enterprise Edition.")
}

// initFederationRules in OSS: no-op.
func initFederationRules() []FederationRule {
	return nil
}
