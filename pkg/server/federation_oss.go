//go:build !enterprise

package server

import "net/http"

// IsFederationSupported flags if federation is supported in this build.
const IsFederationSupported = false

func (s *Server) routeToFederationPeer(w http.ResponseWriter, r *http.Request, subdomain string) bool {
	// Federation is disabled in the Open-Source Edition
	return false
}
