package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockS3Server returns an httptest.Server that acts as a minimal S3-compatible
// cold-storage endpoint. It stores PUT bodies in memory and returns them on GET.
func mockS3Server(t *testing.T) (*httptest.Server, map[string][]byte) {
	t.Helper()
	store := make(map[string][]byte)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read error", http.StatusInternalServerError)
				return
			}
			store[key] = data
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			data, ok := store[key]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func TestColdTier_SweepAndRehydrate(t *testing.T) {
	// 1. Set up the store
	tmpDir, err := os.MkdirTemp("", "servstore-cold-tier-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := NewLocalStore(tmpDir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	bucket := "cold-bucket"
	if err := s.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Enable CAS so objects are stored as cas-<hash>
	if err := s.SetBucketContentAddressable(ctx, bucket, true); err != nil {
		t.Fatalf("SetBucketContentAddressable: %v", err)
	}

	// 2. Upload two objects
	content1 := []byte("cold block data for archival test")
	content2 := []byte("second cold block data")

	ov1, err := s.PutObject(ctx, bucket, "cold1.txt", bytes.NewReader(content1), int64(len(content1)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject 1: %v", err)
	}
	_, err = s.PutObject(ctx, bucket, "cold2.txt", bytes.NewReader(content2), int64(len(content2)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject 2: %v", err)
	}

	// 3. Resolve the CAS data file path for object 1 and backdate its mtime
	//    so the sweep considers it "old enough" even with MinAgeDays=0.
	dataPath := s.getObjectDataPath(bucket, "cold1.txt", ov1.VersionID)
	pastTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dataPath, pastTime, pastTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	// 4. Set up mock remote + configure cold tier with MinAgeDays=1 (anything >24h is cold)
	remoteSrv, remoteStore := mockS3Server(t)

	cfg := ColdTierConfig{
		Endpoint:        remoteSrv.URL,
		RemoteBucket:    "remote-cold",
		MinAgeDays:      1,
		ScanIntervalMin: 0, // background sweep disabled for test — we call manually
	}
	if err := s.SetColdTier(cfg); err != nil {
		t.Fatalf("SetColdTier: %v", err)
	}

	// 5. Run manual sweep
	archived, errs := s.RunColdSweep(ctx)
	if len(errs) > 0 {
		t.Fatalf("sweep errors: %v", errs)
	}
	if archived < 1 {
		t.Fatalf("expected at least 1 block archived, got %d", archived)
	}

	// 6. Verify stub exists and data file is gone for cold1.txt
	stubFile := stubPath(dataPath)
	if _, err := os.Stat(stubFile); err != nil {
		t.Fatalf("expected .cold stub at %s: %v", stubFile, err)
	}
	if _, err := os.Stat(dataPath); err == nil {
		t.Errorf("expected data file %s to be removed after archival", dataPath)
	}

	// 7. Verify the block was uploaded to the mock remote
	hash := strings.TrimPrefix(filepath.Base(dataPath), "cas.")
	remoteKey := "/remote-cold/cas-" + hash
	if _, ok := remoteStore[remoteKey]; !ok {
		t.Errorf("expected key %q in mock remote store, got keys: %v", remoteKey, keys(remoteStore))
	}

	// 8. Transparent re-hydration: GetObject should fetch back and restore the file
	rc, gotVer, err := s.GetObject(ctx, bucket, "cold1.txt", "")
	if err != nil {
		t.Fatalf("GetObject after archival: %v", err)
	}
	defer rc.Close()

	gotData, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read GetObject: %v", err)
	}
	if !bytes.Equal(gotData, content1) {
		t.Errorf("re-hydrated content mismatch: expected %q, got %q", content1, gotData)
	}
	if gotVer.Key != "cold1.txt" {
		t.Errorf("unexpected key in version: %s", gotVer.Key)
	}

	// 9. After re-hydration, stub must be gone and data file must be back
	if _, err := os.Stat(stubFile); err == nil {
		t.Error("stub should have been removed after re-hydration")
	}
	if _, err := os.Stat(dataPath); err != nil {
		t.Errorf("data file should be restored after re-hydration: %v", err)
	}
}

func TestColdTier_GetColdTierConfig(t *testing.T) {
	tmpDir := t.TempDir()
	s, _ := NewLocalStore(tmpDir)
	defer s.Close()

	// Before setting, should return false
	_, ok := s.GetColdTierConfig()
	if ok {
		t.Error("expected GetColdTierConfig to return false before SetColdTier")
	}

	cfg := ColdTierConfig{Endpoint: "http://remote:9000", RemoteBucket: "archival", MinAgeDays: 30}
	_ = s.SetColdTier(cfg)

	gotCfg, ok := s.GetColdTierConfig()
	if !ok {
		t.Error("expected GetColdTierConfig to return true after SetColdTier")
	}
	if gotCfg.Endpoint != cfg.Endpoint || gotCfg.RemoteBucket != cfg.RemoteBucket {
		t.Errorf("config mismatch: got %+v", gotCfg)
	}
}

// keys returns all keys of a string-keyed map, for error messages.
func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
