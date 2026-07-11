//go:build enterprise

package main

import "net/http"

// IsSSLOffloadingSupported indicates if hardware SSL/TLS offloading is supported.
const IsSSLOffloadingSupported = true

// EnterpriseListenAndServeTLS is overridden in the private servverse-ee overlay.
var EnterpriseListenAndServeTLS func(srv *http.Server, certFile, keyFile string) error
