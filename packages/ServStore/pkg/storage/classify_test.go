package storage

import (
	"context"
	"bytes"
	"os"
	"testing"
)

func TestAutoClassifyUnit(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		contentType string
		content     []byte
		expected    string
	}{
		{
			name:        "Invoice by content keyword",
			key:         "doc.pdf",
			contentType: "application/pdf",
			content:     []byte("Billing statement for invoice #12345"),
			expected:    "invoice",
		},
		{
			name:        "Contract by key name",
			key:         "service-agreement-final.txt",
			contentType: "text/plain",
			content:     []byte("This is a simple doc"),
			expected:    "contract",
		},
		{
			name:        "Log by content pattern",
			key:         "app.out",
			contentType: "text/plain",
			content:     []byte("[INFO] 2026-07-08 Started service"),
			expected:    "log",
		},
		{
			name:        "Image by content type",
			key:         "photo",
			contentType: "image/png",
			content:     []byte("raw-bytes"),
			expected:    "image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := AutoClassify(tt.key, tt.contentType, tt.content)
			if tags["ai-class"] != tt.expected {
				t.Errorf("expected class %q, got %q", tt.expected, tags["ai-class"])
			}
			if tags["ai-confidence"] == "" {
				t.Error("expected confidence score, got empty")
			}
		})
	}
}

func TestAutoClassifyIntegration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-classify-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "test-classify-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// Put an invoice
	invContent := []byte("This is the invoice summary for payment processing.")
	ov, err := store.PutObject(ctx, bucket, "my-invoice.txt", bytes.NewReader(invContent), int64(len(invContent)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	if ov.Tags == nil {
		t.Fatal("expected tags to be populated, got nil")
	}

	if ov.Tags["ai-class"] != "invoice" {
		t.Errorf("expected tag ai-class=invoice, got %q", ov.Tags["ai-class"])
	}

	// Verify we can retrieve tags
	tags, err := store.GetObjectTagging(ctx, bucket, "my-invoice.txt", ov.VersionID)
	if err != nil {
		t.Fatalf("GetObjectTagging failed: %v", err)
	}

	if tags["ai-class"] != "invoice" {
		t.Errorf("expected retrieved tag ai-class=invoice, got %q", tags["ai-class"])
	}
}
