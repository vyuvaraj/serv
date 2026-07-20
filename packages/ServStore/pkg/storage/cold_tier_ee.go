//go:build enterprise

package storage

// IsIntelligentTieringSupported indicates if Glacier and lifecycle auto-tiering is supported.
const IsIntelligentTieringSupported = true

// Note: The actual background Glacier tiering and lifecycle scan implementations
// reside in the private servverse-ee repository and overlay here at build time.
