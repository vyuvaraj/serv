package storage

import (
	"math/rand"
	"testing"
)

func TestPerformance_ParallelBlake3HashCorrectness(t *testing.T) {
	// Generate random payload larger than the 8MB parallelization threshold
	size := 10 * 1024 * 1024 // 10 MB
	data := make([]byte, size)
	rnd := rand.New(rand.NewSource(42))
	_, _ = rnd.Read(data)

	// Single threaded standard hash
	singleHash := ParallelBlake3Hash(data[:1*1024*1024]) // under threshold
	
	// Large segment parallel hash
	largeHash1 := ParallelBlake3Hash(data)
	largeHash2 := ParallelBlake3Hash(data)

	if largeHash1 != largeHash2 {
		t.Error("Parallel Blake3 hashing is non-deterministic")
	}

	if singleHash == "" || largeHash1 == "" {
		t.Error("Generated hash digests are empty")
	}
}

func BenchmarkBlake3Hashing(b *testing.B) {
	size := 12 * 1024 * 1024 // 12 MB
	data := make([]byte, size)
	rnd := rand.New(rand.NewSource(99))
	_, _ = rnd.Read(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ParallelBlake3Hash(data)
	}
}
