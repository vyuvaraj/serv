//go:build !enterprise

package main

import "net/http"

// IsSSLOffloadingSupported indicates if hardware SSL/TLS offloading is supported.
const IsSSLOffloadingSupported = false

// SetupSSLOffloading is the open-source stub. It always returns false,
// meaning standard TLS Listeners are used instead of hardware offloaders.
func SetupSSLOffloading(_ *http.Server) bool {
	return false
}
