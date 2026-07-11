//go:build enterprise

package storage

// IsZeroCopySupported indicates if zero-copy serialization is supported.
const IsZeroCopySupported = true

// Note: The actual direct kernel ring buffer / zero-copy memory-mapped WAL serialization
// logic is implemented in the servverse-ee repository and overlays here at build time.
