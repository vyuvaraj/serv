//go:build !enterprise

package main

import "net/http"

// IsSSLOffloadingSupported indicates if hardware SSL/TLS offloading is supported.
const IsSSLOffloadingSupported = false

// EnterpriseListenAndServeTLS serves as the fallback standard Go TLS listener in OSS.
func EnterpriseListenAndServeTLS(srv *http.Server, certFile, keyFile string) error {
	return srv.ListenAndServeTLS(certFile, keyFile)
}
