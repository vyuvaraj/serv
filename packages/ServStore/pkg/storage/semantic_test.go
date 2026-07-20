package storage

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
)

func TestSemanticSearch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-semantic-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucketName := "semantic-bucket"
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// 1. Upload Object 1 (Raft/Consensus)
	doc1 := []byte("Distributed storage engines use consensus algorithms like Raft to replicate metadata consistently.")
	_, err = store.PutObject(ctx, bucketName, "raft-doc.txt", bytes.NewReader(doc1), int64(len(doc1)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put doc1: %v", err)
	}

	// 2. Upload Object 2 (Baking recipe)
	doc2 := []byte("Baking bread requires flour, water, yeast, and salt mixed and baked in a hot oven.")
	_, err = store.PutObject(ctx, bucketName, "recipe.txt", bytes.NewReader(doc2), int64(len(doc2)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put doc2: %v", err)
	}

	// 3. Upload Object 3 (Vector Similarity embeddings)
	doc3 := []byte("Machine learning systems generate embeddings to compute cosine similarity.")
	_, err = store.PutObject(ctx, bucketName, "ml-embeddings.txt", bytes.NewReader(doc3), int64(len(doc3)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put doc3: %v", err)
	}

	// 4. Test Query 1: "consensus algorithms and metadata replication"
	results1, err := store.SemanticSearch(ctx, bucketName, "consensus algorithms and metadata replication", 5)
	if err != nil {
		t.Fatalf("semantic search failed: %v", err)
	}
	if len(results1) == 0 {
		t.Fatal("expected at least one result, got 0")
	}
	if results1[0].Key != "raft-doc.txt" {
		t.Errorf("expected top match to be 'raft-doc.txt', got %s", results1[0].Key)
	}

	// 5. Test Query 2: "yeast and baking recipe"
	results2, err := store.SemanticSearch(ctx, bucketName, "yeast and baking recipe", 5)
	if err != nil {
		t.Fatalf("semantic search failed: %v", err)
	}
	if len(results2) == 0 {
		t.Fatal("expected at least one result, got 0")
	}
	if results2[0].Key != "recipe.txt" {
		t.Errorf("expected top match to be 'recipe.txt', got %s", results2[0].Key)
	}
}

func TestHNSWProperties(t *testing.T) {
	// 1. Test embedding properties
	embedding := GenerateEmbedding("This is a simple sentence to check embedding dimensions and L2 normalization.")
	if len(embedding) != 128 {
		t.Fatalf("expected embedding dimension 128, got %d", len(embedding))
	}
	sumSq := 0.0
	for _, val := range embedding {
		sumSq += val * val
	}
	if math.Abs(sumSq-1.0) > 1e-9 {
		t.Errorf("expected normalized vector sum of squares to be 1.0, got %f", sumSq)
	}

	// 2. Test HNSW indexing directly
	idx := NewHNSWIndex()
	v1 := GenerateEmbedding("machine learning")
	v2 := GenerateEmbedding("deep neural networks")
	v3 := GenerateEmbedding("baking chocolate chip cookies")

	idx.Insert("ml", v1)
	idx.Insert("nn", v2)
	idx.Insert("cookies", v3)

	// Search closest to "machine learning neural networks"
	queryVec := GenerateEmbedding("neural networks machine learning")
	results := idx.Search(queryVec, 2)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Key == "cookies" {
		t.Errorf("unexpected top result: %s", results[0].Key)
	}

	// 3. Test deletion from index
	idx.Insert("cookies", v3)
	idx.mu.Lock()
	idx.deleteNodeNoLock("cookies")
	idx.mu.Unlock()

	resultsAfterDelete := idx.Search(queryVec, 3)
	for _, r := range resultsAfterDelete {
		if r.Key == "cookies" {
			t.Error("expected 'cookies' to be deleted from the index")
		}
	}
}

func BenchmarkHNSWvsLinearSearch(b *testing.B) {
	// Initialize index
	idx := NewHNSWIndex()

	// Create 500 random vectors of 128 dimensions
	numVectors := 500
	dim := 128
	keys := make([]string, numVectors)
	vectors := make([][]float64, numVectors)

	for i := 0; i < numVectors; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
		v := make([]float64, dim)
		sumSq := 0.0
		for j := 0; j < dim; j++ {
			// Generate pseudo-random numbers
			v[j] = math.Sin(float64(i*dim + j))
			sumSq += v[j] * v[j]
		}
		// Normalize
		norm := math.Sqrt(sumSq)
		for j := 0; j < dim; j++ {
			v[j] /= norm
		}
		vectors[i] = v
		idx.Insert(keys[i], v)
	}

	// Generate a query vector
	query := make([]float64, dim)
	sumSq := 0.0
	for j := 0; j < dim; j++ {
		query[j] = math.Cos(float64(j))
		sumSq += query[j] * query[j]
	}
	norm := math.Sqrt(sumSq)
	for j := 0; j < dim; j++ {
		query[j] /= norm
	}

	b.ResetTimer()

	b.Run("HNSW_Search", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = idx.Search(query, 5)
		}
	})

	b.Run("Linear_Search_Scan", func(b *testing.B) {
		type distKey struct {
			key  string
			dist float64
		}
		for i := 0; i < b.N; i++ {
			var list []distKey
			for k := 0; k < numVectors; k++ {
				dist := CosineDistance(query, vectors[k])
				list = append(list, distKey{key: keys[k], dist: dist})
			}
			sort.Slice(list, func(x, y int) bool {
				return list[x].dist < list[y].dist
			})
			_ = list[:5]
		}
	})
}
