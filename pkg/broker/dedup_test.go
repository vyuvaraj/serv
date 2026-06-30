package broker

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkDeduplicatorAdd(b *testing.B) {
	d := NewDeduplicator(10 * time.Second)
	
	// Pre-populate with some IDs
	for i := 0; i < 1000; i++ {
		d.Add(fmt.Sprintf("msg-id-%d", i))
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("msg-id-%d", 1000 + (i % 1000))
		_ = d.Add(id)
	}
}
