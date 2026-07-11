//go:build enterprise

package server

import (
	"fmt"
	"net/http"
	"time"
)

// IsFederationSupported flags if federation is supported in this build.
const IsFederationSupported = true

func (s *Server) routeToFederationPeer(w http.ResponseWriter, r *http.Request, subdomain string) bool {
	if len(s.federationPeers) > 0 {
		for _, peer := range s.federationPeers {
			checkURL := fmt.Sprintf("%s/api/tunnels/%s/exists", peer, subdomain)
			reqCheck, err := http.NewRequestWithContext(r.Context(), "HEAD", checkURL, nil)
			if err != nil {
				continue
			}
			client := &http.Client{Timeout: 1 * time.Second}
			respCheck, err := client.Do(reqCheck)
			if err == nil {
				respCheck.Body.Close()
				if respCheck.StatusCode == http.StatusOK {
					s.proxyToPeer(w, r, peer)
					return true
				}
			}
		}
	}
	return false
}
