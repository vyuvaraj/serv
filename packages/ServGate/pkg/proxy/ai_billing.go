//go:build enterprise

package proxy

// IsVectorSearchAccelerated flags if GPU/SIMD vector acceleration is supported.
const IsVectorSearchAccelerated = true

// IsSemanticCacheSupported flags if the semantic cache is supported in this build.
const IsSemanticCacheSupported = true

// Note: The actual GPU-accelerated HNSW indexing and SIMD vector optimization (AVX-512)
// implementations will reside in the private servverse-ee repository and overlay here.
