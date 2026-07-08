package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestS3BatchOperations(t *testing.T) {
	// Initialize store
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer store.Close()

	// Initialize Gateway (with batchMgr initialized automatically in NewGateway)
	authProvider := auth.NewAuthProvider("", "", false)
	gateway := NewGateway(store, authProvider, nil, nil, 1, false, 0, 0)

	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{}
	defer client.CloseIdleConnections()

	ctx := context.Background()

	// Helper to create bucket
	createBucket := func(bucketName string) {
		req, err := http.NewRequest("PUT", fmt.Sprintf("%s/%s", server.URL, bucketName), nil)
		if err != nil {
			t.Fatalf("failed to create bucket PUT request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send bucket PUT request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected bucket creation status 200, got %d", resp.StatusCode)
		}
	}

	// Helper to put object
	putObject := func(bucketName, keyName, content, contentType string) {
		req, err := http.NewRequest("PUT", fmt.Sprintf("%s/%s/%s", server.URL, bucketName, keyName), bytes.NewReader([]byte(content)))
		if err != nil {
			t.Fatalf("failed to create object PUT request: %v", err)
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send object PUT request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected object PUT status 200, got %d", resp.StatusCode)
		}
	}

	// Helper to verify object content
	verifyObject := func(bucketName, keyName, expectedContent, expectedContentType string) {
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s/%s", server.URL, bucketName, keyName), nil)
		if err != nil {
			t.Fatalf("failed to create object GET request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send object GET request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected object GET status 200, got %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("failed to read object content: %v", err)
		}
		if string(data) != expectedContent {
			t.Errorf("expected content %q, got %q", expectedContent, string(data))
		}
		if expectedContentType != "" {
			ct := resp.Header.Get("Content-Type")
			if ct != expectedContentType {
				t.Errorf("expected Content-Type %q, got %q", expectedContentType, ct)
			}
		}
	}

	// Helper to verify object deleted
	verifyObjectDeleted := func(bucketName, keyName string) {
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s/%s", server.URL, bucketName, keyName), nil)
		if err != nil {
			t.Fatalf("failed to create object GET request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send object GET request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404 for deleted object, got %d", resp.StatusCode)
		}
	}

	// Helper to submit and wait for batch job completion
	runBatchJob := func(op, bucket string, keys []string, params map[string]string) *BatchJob {
		bodyMap := map[string]interface{}{
			"operation": op,
			"bucket":    bucket,
			"objects":   keys,
			"params":    params,
		}
		bodyBytes, _ := json.Marshal(bodyMap)

		req, err := http.NewRequest("POST", fmt.Sprintf("%s/admin/batch", server.URL), bytes.NewReader(bodyBytes))
		if err != nil {
			t.Fatalf("failed to create POST batch request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to send POST batch request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			respBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected status 202 Accepted, got %d. Body: %s", resp.StatusCode, string(respBytes))
		}

		var submitResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
			t.Fatalf("failed to decode submit response: %v", err)
		}

		jobID := submitResp["job_id"]
		if jobID == "" {
			t.Fatalf("job_id should not be empty")
		}

		// Poll for job completion
		var finalJob BatchJob
		for i := 0; i < 20; i++ {
			reqGet, err := http.NewRequest("GET", fmt.Sprintf("%s/admin/batch/%s", server.URL, jobID), nil)
			if err != nil {
				t.Fatalf("failed to create GET job request: %v", err)
			}
			respGet, err := client.Do(reqGet)
			if err != nil {
				t.Fatalf("failed to send GET job request: %v", err)
			}
			if respGet.StatusCode != http.StatusOK {
				respGet.Body.Close()
				t.Fatalf("expected GET job status 200, got %d", respGet.StatusCode)
			}
			err = json.NewDecoder(respGet.Body).Decode(&finalJob)
			respGet.Body.Close()
			if err != nil {
				t.Fatalf("failed to decode job status: %v", err)
			}

			if finalJob.Status == "Completed" || finalJob.Status == "Failed" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		return &finalJob
	}

	// 1. Create source and target buckets
	createBucket("src-bucket")
	createBucket("dst-bucket")

	// 2. Put initial objects
	putObject("src-bucket", "obj1.txt", "content-1", "text/plain")
	putObject("src-bucket", "obj2.txt", "content-2", "text/plain")

	// 3. Test Bulk Copy Operation
	t.Run("Bulk Copy", func(t *testing.T) {
		job := runBatchJob("Copy", "src-bucket", []string{"obj1.txt", "obj2.txt"}, map[string]string{
			"target_bucket": "dst-bucket",
		})
		if job.Status != "Completed" {
			t.Errorf("expected job status Completed, got %s (error: %s)", job.Status, job.Error)
		}
		if job.ProcessedCount != 2 {
			t.Errorf("expected 2 processed objects, got %d", job.ProcessedCount)
		}
		if job.FailedCount != 0 {
			t.Errorf("expected 0 failed objects, got %d", job.FailedCount)
		}

		// Verify objects in destination bucket
		verifyObject("dst-bucket", "obj1.txt", "content-1", "text/plain")
		verifyObject("dst-bucket", "obj2.txt", "content-2", "text/plain")
	})

	// 4. Test Bulk Tagging Operation
	t.Run("Bulk Tagging", func(t *testing.T) {
		tagsMap := map[string]string{"project": "servstore", "env": "test"}
		tagsJSON, _ := json.Marshal(tagsMap)

		job := runBatchJob("Tagging", "src-bucket", []string{"obj1.txt", "obj2.txt"}, map[string]string{
			"tags": string(tagsJSON),
		})
		if job.Status != "Completed" {
			t.Errorf("expected job status Completed, got %s (error: %s)", job.Status, job.Error)
		}

		// Verify tagging on storage level
		tags1, err := store.GetObjectTagging(ctx, "src-bucket", "obj1.txt", "")
		if err != nil {
			t.Fatalf("failed to get tags: %v", err)
		}
		if tags1["project"] != "servstore" || tags1["env"] != "test" {
			t.Errorf("unexpected tags: %v", tags1)
		}
	})

	// 5. Test Bulk Metadata (Content-Type) Update
	t.Run("Bulk Metadata Update", func(t *testing.T) {
		job := runBatchJob("Metadata", "src-bucket", []string{"obj1.txt", "obj2.txt"}, map[string]string{
			"content_type": "application/octet-stream",
		})
		if job.Status != "Completed" {
			t.Errorf("expected job status Completed, got %s (error: %s)", job.Status, job.Error)
		}

		// Verify Content-Type updated
		verifyObject("src-bucket", "obj1.txt", "content-1", "application/octet-stream")
		verifyObject("src-bucket", "obj2.txt", "content-2", "application/octet-stream")
	})

	// 6. Test Bulk Delete Operation
	t.Run("Bulk Delete", func(t *testing.T) {
		job := runBatchJob("Delete", "src-bucket", []string{"obj1.txt", "obj2.txt"}, nil)
		if job.Status != "Completed" {
			t.Errorf("expected job status Completed, got %s (error: %s)", job.Status, job.Error)
		}

		// Verify objects deleted
		verifyObjectDeleted("src-bucket", "obj1.txt")
		verifyObjectDeleted("src-bucket", "obj2.txt")
	})
}
