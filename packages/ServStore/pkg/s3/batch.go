package s3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"servstore/pkg/storage"
)

type BatchJob struct {
	JobID          string            `json:"job_id"`
	Status         string            `json:"status"` // "Pending", "Running", "Completed", "Failed"
	Operation      string            `json:"operation"` // "Copy", "Delete", "Tagging", "Metadata"
	Bucket         string            `json:"bucket"`
	Objects        []string          `json:"objects"`
	Params         map[string]string `json:"params,omitempty"`
	TotalObjects   int               `json:"total_objects"`
	ProcessedCount int               `json:"processed_count"`
	FailedCount    int               `json:"failed_count"`
	Error          string            `json:"error,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

type BatchJobManager struct {
	mu    sync.RWMutex
	jobs  map[string]*BatchJob
	store storage.StorageEngine
}

func NewBatchJobManager(store storage.StorageEngine) *BatchJobManager {
	return &BatchJobManager{
		jobs:  make(map[string]*BatchJob),
		store: store,
	}
}

func (bm *BatchJobManager) SubmitJob(operation, bucket string, objects []string, params map[string]string) string {
	jobID := generateUUID()
	job := &BatchJob{
		JobID:        jobID,
		Status:       "Pending",
		Operation:    operation,
		Bucket:       bucket,
		Objects:      objects,
		Params:       params,
		TotalObjects: len(objects),
		CreatedAt:    time.Now(),
	}

	bm.mu.Lock()
	bm.jobs[jobID] = job
	bm.mu.Unlock()

	go bm.executeJob(job)
	return jobID
}

func (bm *BatchJobManager) GetJob(jobID string) (*BatchJob, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	job, exists := bm.jobs[jobID]
	if !exists {
		return nil, false
	}
	// Return a copy to avoid race conditions
	jobCopy := *job
	return &jobCopy, true
}

func (bm *BatchJobManager) executeJob(job *BatchJob) {
	bm.mu.Lock()
	job.Status = "Running"
	bm.mu.Unlock()

	ctx := context.Background()

	for _, key := range job.Objects {
		err := bm.processObject(ctx, job.Operation, job.Bucket, key, job.Params)
		bm.mu.Lock()
		if err != nil {
			job.FailedCount++
		} else {
			job.ProcessedCount++
		}
		bm.mu.Unlock()
	}

	bm.mu.Lock()
	now := time.Now()
	job.CompletedAt = &now
	if job.FailedCount == job.TotalObjects && job.TotalObjects > 0 {
		job.Status = "Failed"
		job.Error = "All batch operations failed"
	} else {
		job.Status = "Completed"
	}
	bm.mu.Unlock()
}

func (bm *BatchJobManager) processObject(ctx context.Context, op, bucket, key string, params map[string]string) error {
	switch strings.ToLower(op) {
	case "copy":
		targetBucket := params["target_bucket"]
		if targetBucket == "" {
			return fmt.Errorf("missing target_bucket parameter")
		}
		rc, ov, err := bm.store.GetObject(ctx, bucket, key, "")
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = bm.store.PutObject(ctx, targetBucket, key, rc, ov.Size, ov.ContentType)
		return err

	case "delete":
		_, err := bm.store.DeleteObject(ctx, bucket, key, "")
		return err

	case "tagging":
		tagsJSON := params["tags"]
		var tags map[string]string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return err
		}
		_, err := bm.store.PutObjectTagging(ctx, bucket, key, "", tags)
		return err

	case "metadata":
		contentType := params["content_type"]
		if contentType == "" {
			return fmt.Errorf("missing content_type parameter")
		}
		// Read and put back with new content type
		rc, ov, err := bm.store.GetObject(ctx, bucket, key, "")
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = bm.store.PutObject(ctx, bucket, key, rc, ov.Size, contentType)
		return err

	default:
		return fmt.Errorf("unsupported batch operation: %s", op)
	}
}

// REST HTTP Handlers registered on S3 Server Gateway
func (g *Gateway) handleCreateBatchJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Operation string            `json:"operation"`
		Bucket    string            `json:"bucket"`
		Objects   []string          `json:"objects"`
		Params    map[string]string `json:"params"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidJSON", "Failed to decode request body.")
		return
	}

	if req.Operation == "" || req.Bucket == "" || len(req.Objects) == 0 {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidArgument", "operation, bucket, and non-empty objects list are required")
		return
	}

	jobID := g.batchMgr.SubmitJob(req.Operation, req.Bucket, req.Objects, req.Params)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": jobID, "status": "Pending"})
}

func (g *Gateway) handleGetBatchJob(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidArgument", "missing job id")
		return
	}
	jobID := parts[3]

	job, exists := g.batchMgr.GetJob(jobID)
	if !exists {
		g.writeErrorCtx(w, r, http.StatusNotFound, "NoSuchJob", "The specified batch job does not exist")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(job)
}
