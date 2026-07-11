//go:build enterprise

package main



// IsSSLOffloadingSupported indicates if hardware SSL/TLS offloading is supported.
const IsSSLOffloadingSupported = true

// Note: The actual cryptoprocessor NIC offloading / hardware acceleration listener config
// logic resides in the private servverse-ee repository and overlays here at build time.
