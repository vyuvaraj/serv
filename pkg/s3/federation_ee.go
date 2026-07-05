//go:build enterprise

package s3

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// resolveFederatedBucket in EE: resolves bucket to remote cluster if a federation rule matches.
func (g *Gateway) resolveFederatedBucket(bucket string) (string, bool) {
	if bucket == "system-access-logs" {
		return "", false
	}

	targetRegion := ""
	if strings.Contains(bucket, "@") {
		parts := strings.SplitN(bucket, "@", 2)
		bucket = parts[0]
		targetRegion = parts[1]
	}

	g.fedMutex.RLock()
	defer g.fedMutex.RUnlock()

	for _, rule := range g.federationRules {
		matches := false
		if targetRegion != "" {
			matches = strings.EqualFold(rule.Pattern, targetRegion)
		} else {
			matches = matchPattern(bucket, rule.Pattern)
		}

		if matches {
			if g.cluster != nil {
				localAddr, ok := g.cluster.GetNodeAddress(g.cluster.LocalNodeID())
				if ok && (rule.Target == localAddr || strings.Contains(rule.Target, localAddr)) {
					continue
				}
			}
			return rule.Target, true
		}
	}
	return "", false
}

// handleRegisterFederation in EE: registers a new federation routing rule.
func (g *Gateway) handleRegisterFederation(w http.ResponseWriter, r *http.Request) {
	var rule FederationRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidJSON", "Failed to decode federation rule.")
		return
	}

	if rule.Pattern == "" || rule.Target == "" {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidArgument", "pattern and target fields are required")
		return
	}

	g.fedMutex.Lock()
	g.federationRules = append(g.federationRules, rule)
	g.fedMutex.Unlock()

	slog.Info("Federation rule registered", "pattern", rule.Pattern, "target", rule.Target)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Federation rule registered successfully"))
}

// initFederationRules in EE: parses SERVSTORE_FEDERATION_MAP env var.
func initFederationRules() []FederationRule {
	var rules []FederationRule
	if fedMap := os.Getenv("SERVSTORE_FEDERATION_MAP"); fedMap != "" {
		for _, part := range strings.Split(fedMap, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			subParts := strings.SplitN(part, "=", 2)
			if len(subParts) == 2 {
				rules = append(rules, FederationRule{
					Pattern: strings.TrimSpace(subParts[0]),
					Target:  strings.TrimSpace(subParts[1]),
				})
			}
		}
	}
	return rules
}
