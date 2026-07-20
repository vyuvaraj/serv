//go:build enterprise

package main

// IsSSLOffloadingSupported indicates if hardware SSL/TLS offloading is supported.
const IsSSLOffloadingSupported = true

// Note: EnterpriseListenAndServeTLS variable declared in main.go is overridden
// in the private servverse-ee overlay at build time.
