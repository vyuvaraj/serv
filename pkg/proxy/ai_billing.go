//go:build enterprise

package proxy

// IsVectorSearchAccelerated flags if GPU/SIMD vector acceleration is supported.
const IsVectorSearchAccelerated = true

// Note: The actual GPU-accelerated HNSW indexing and SIMD vector optimization (AVX-512)
// implementations will reside in the private servverse-ee repository and overlay here.
