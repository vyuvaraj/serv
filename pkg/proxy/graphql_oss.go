//go:build !enterprise

package proxy

import (
	"net/http"
)

func (h *GatewayHandler) handleGraphQLFederation(w http.ResponseWriter, r *http.Request, _ *Route) {
	WriteJSONError(w, r, "GraphQL Federation requires ServGate Enterprise Edition", "ERR_EE_REQUIRED", http.StatusForbidden)
}
