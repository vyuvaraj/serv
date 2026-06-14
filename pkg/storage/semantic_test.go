package storage

import (
	"bytes"
	"context"
	"os"
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
