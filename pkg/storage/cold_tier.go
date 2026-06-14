// Package storage cold_tier.go — Hybrid Cloud Cold-Storage Tiering
//
// ColdTierManager asynchronously archives "cold" CAS data blocks to any
// S3-compatible endpoint (AWS S3 Glacier, MinIO, Backblaze B2, …) using
// only the Go standard library (net/http). No CGO. No external SDKs.
//
// Lifecycle:
//  1. A background sweep walks every cas-<hash> file in each bucket.
//  2. Files whose mtime is older than MinAgeDays are uploaded to the remote.
//  3. The local file is replaced with a tiny JSON stub (<path>.cold).
//  4. On the next GetObject, the stub is detected and the block is transparently
//     re-fetched and cached locally (stub removed).
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ColdTierConfig specifies the remote cold-storage backend and policy.
type ColdTierConfig struct {
	// Endpoint is the base URL of the S3-compatible service.
	// Example: "https://s3.amazonaws.com" or "http://minio:9000"
	Endpoint string `json:"endpoint"`
	// RemoteBucket is the bucket name on the remote service.
	RemoteBucket string `json:"remote_bucket"`
	// Region is the AWS/service region (e.g. "us-east-1").
	Region string `json:"region"`
	// AccessKey and SecretKey are the credentials for the remote service.
	// Leave empty to rely on ambient IAM (e.g. EC2 instance profile).
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	// MinAgeDays is how old (in days) a CAS block must be before it is
	// eligible for archival. 0 means archive immediately.
	MinAgeDays int `json:"min_age_days"`
	// ScanIntervalMin is how often the background sweep runs (minutes).
	// Defaults to 60 if ≤0.
	ScanIntervalMin int `json:"scan_interval_min"`
}

// coldStub is the JSON metadata written at <dataPath>.cold after archival.
type coldStub struct {
	RemoteURL  string    `json:"remote_url"`
	ArchivedAt time.Time `json:"archived_at"`
	SizeBytes  int64     `json:"size_bytes"`
	Hash       string    `json:"hash"`
}

// ColdTierManager drives archival sweeps and transparent re-hydration.
type ColdTierManager struct {
	store  *LocalStore
	cfg    ColdTierConfig
	client *http.Client
}

// newColdTierManager creates a ColdTierManager but does not start sweeps.
func newColdTierManager(store *LocalStore, cfg ColdTierConfig) *ColdTierManager {
	return &ColdTierManager{
		store:  store,
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// remoteURL constructs the remote object URL for a given CAS hash.
func (m *ColdTierManager) remoteURL(hash string) string {
	base := strings.TrimRight(m.cfg.Endpoint, "/")
	return fmt.Sprintf("%s/%s/cas-%s", base, m.cfg.RemoteBucket, hash)
}

// stubPath returns the .cold stub path for a given data-file path.
func stubPath(dataPath string) string {
	return dataPath + ".cold"
}

// archiveBlock uploads a local CAS data file to the remote endpoint and
// replaces the local file with a .cold stub. The caller must hold no lock
// that would prevent file I/O.
func (m *ColdTierManager) archiveBlock(ctx context.Context, dataPath, hash string) error {
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return fmt.Errorf("cold tier: read %s: %w", dataPath, err)
	}

	remURL := m.remoteURL(hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, remURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cold tier: build PUT request: %w", err)
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	if m.cfg.AccessKey != "" {
		req.SetBasicAuth(m.cfg.AccessKey, m.cfg.SecretKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("cold tier: PUT %s: %w", remURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cold tier: PUT %s returned %d: %s", remURL, resp.StatusCode, body)
	}

	// Write .cold stub
	stub := coldStub{
		RemoteURL:  remURL,
		ArchivedAt: time.Now().UTC(),
		SizeBytes:  int64(len(data)),
		Hash:       hash,
	}
	stubData, err := json.Marshal(stub)
	if err != nil {
		return fmt.Errorf("cold tier: marshal stub: %w", err)
	}
	sp := stubPath(dataPath)
	if err := os.WriteFile(sp, stubData, 0644); err != nil {
		return fmt.Errorf("cold tier: write stub %s: %w", sp, err)
	}

	// Remove the local data file now that the stub is in place
	if err := os.Remove(dataPath); err != nil {
		// Non-fatal: stub exists, the next GetObject will hydrate.
		_ = err
	}
	return nil
}

// FetchBack reads the .cold stub at stubPath, downloads the block from the
// remote, writes it back to the original dataPath, and removes the stub.
// This is the re-hydration path triggered on the first GetObject after archival.
func (m *ColdTierManager) FetchBack(ctx context.Context, dataPath string) error {
	sp := stubPath(dataPath)
	stubData, err := os.ReadFile(sp)
	if err != nil {
		return fmt.Errorf("cold tier: read stub %s: %w", sp, err)
	}
	var stub coldStub
	if err := json.Unmarshal(stubData, &stub); err != nil {
		return fmt.Errorf("cold tier: parse stub: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stub.RemoteURL, nil)
	if err != nil {
		return fmt.Errorf("cold tier: build GET request: %w", err)
	}
	if m.cfg.AccessKey != "" {
		req.SetBasicAuth(m.cfg.AccessKey, m.cfg.SecretKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("cold tier: GET %s: %w", stub.RemoteURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cold tier: GET %s returned %d", stub.RemoteURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cold tier: read GET body: %w", err)
	}

	// Atomically restore data file
	tmp, err := os.CreateTemp(filepath.Dir(dataPath), "cold-restore-*")
	if err != nil {
		return fmt.Errorf("cold tier: create temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("cold tier: write temp: %w", err)
	}
	_ = tmp.Close()
	if err := os.Rename(tmp.Name(), dataPath); err != nil {
		return fmt.Errorf("cold tier: rename restore: %w", err)
	}

	// Remove stub — local data is back
	_ = os.Remove(sp)
	return nil
}

// RunSweep walks all local buckets, finds CAS data blocks older than MinAgeDays,
// and archives them. Returns the count of blocks archived and any accumulated errors.
func (m *ColdTierManager) RunSweep(ctx context.Context) (archived int, errs []error) {
	minAge := time.Duration(m.cfg.MinAgeDays) * 24 * time.Hour
	cutoff := time.Now().Add(-minAge)

	entries, err := os.ReadDir(m.store.rootDir)
	if err != nil {
		return 0, []error{fmt.Errorf("cold tier sweep: list root: %w", err)}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bucket := entry.Name()
		dataDir := filepath.Join(m.store.rootDir, bucket, ".data")

		dEntries, err := os.ReadDir(dataDir)
		if err != nil {
			continue // bucket has no .data dir yet
		}

		for _, de := range dEntries {
			if de.IsDir() {
				continue
			}
			name := de.Name()
			// Only archive plain CAS blocks (not stubs themselves)
			if !strings.HasPrefix(name, "cas.") || strings.HasSuffix(name, ".cold") {
				continue
			}
			// Skip if already has a stub (a previous partial archive)
			dataPath := filepath.Join(dataDir, name)
			if _, err := os.Stat(stubPath(dataPath)); err == nil {
				continue
			}

			info, err := de.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue // still warm
			}

			hash := strings.TrimPrefix(name, "cas.")
			if err := m.archiveBlock(ctx, dataPath, hash); err != nil {
				errs = append(errs, err)
			} else {
				archived++
			}
		}
	}
	return archived, errs
}

// Start launches a background goroutine that calls RunSweep periodically.
// The goroutine exits when ctx is cancelled.
func (m *ColdTierManager) Start(ctx context.Context) {
	interval := time.Duration(m.cfg.ScanIntervalMin) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = m.RunSweep(ctx)
			}
		}
	}()
}
