package storage

import (
	"encoding/hex"
	"runtime"
	"sync"

	"github.com/zeebo/blake3"
)

const chunkThreshold = 8 * 1024 * 1024 // 8 MB

// ParallelBlake3Hash computes the BLAKE3 hash of a data stream concurrently if the size
// exceeds the chunkThreshold (8MB).
func ParallelBlake3Hash(data []byte) string {
	if len(data) <= chunkThreshold {
		h := blake3.New()
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}

	numCPU := runtime.NumCPU()
	if numCPU < 2 {
		numCPU = 2
	}

	chunkSize := (len(data) + numCPU - 1) / numCPU
	hashes := make([][]byte, numCPU)
	var wg sync.WaitGroup

	for i := 0; i < numCPU; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start := idx * chunkSize
			if start >= len(data) {
				return
			}
			end := start + chunkSize
			if end > len(data) {
				end = len(data)
			}

			h := blake3.New()
			h.Write(data[start:end])
			hashes[idx] = h.Sum(nil)
		}(i)
	}

	wg.Wait()

	// Merge all chunk hashes together using BLAKE3 tree reduction logic
	rootHasher := blake3.New()
	for _, chunkHash := range hashes {
		if len(chunkHash) > 0 {
			rootHasher.Write(chunkHash)
		}
	}

	return hex.EncodeToString(rootHasher.Sum(nil))
}
