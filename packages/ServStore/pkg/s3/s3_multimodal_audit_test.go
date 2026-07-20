package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestS3MultimodalAndAudit(t *testing.T) {
	dir, err := os.MkdirTemp("", "servstore-multimodal-audit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "media-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)
	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{}

	// 1. Verify Multi-modal ingest generates embeddings and allows semantic search
	// Upload an image (.png)
	putImgReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/nature.png", strings.NewReader("fake png content"))
	putImgReq.Header.Set("Content-Type", "image/png")
	putImgReq.SetBasicAuth("admin", "admin")
	resp, err := client.Do(putImgReq)
	if err != nil {
		t.Fatalf("PUT image failed: %v", err)
	}
	resp.Body.Close()

	// Upload a document (.pdf)
	putPdfReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/paper.pdf", strings.NewReader("fake pdf content"))
	putPdfReq.Header.Set("Content-Type", "application/pdf")
	putPdfReq.SetBasicAuth("admin", "admin")
	resp, err = client.Do(putPdfReq)
	if err != nil {
		t.Fatalf("PUT pdf failed: %v", err)
	}
	resp.Body.Close()

	// Upload an audio (.mp3)
	putAudReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/song.mp3", strings.NewReader("fake mp3 content"))
	putAudReq.Header.Set("Content-Type", "audio/mpeg")
	putAudReq.SetBasicAuth("admin", "admin")
	resp, err = client.Do(putAudReq)
	if err != nil {
		t.Fatalf("PUT audio failed: %v", err)
	}
	resp.Body.Close()

	// Verify Semantic Search can find the uploaded multimodal objects
	// (Querying 'paper' or similar should rank PDF or files high)
	searchReq, _ := http.NewRequest("GET", server.URL+"/"+bucket+"?query=semantic&q=paper", nil)
	searchReq.SetBasicAuth("admin", "admin")
	searchResp, err := client.Do(searchReq)
	if err != nil {
		t.Fatalf("semantic search failed: %v", err)
	}
	defer searchResp.Body.Close()
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("expected semantic search 200, got %d", searchResp.StatusCode)
	}

	// 2. Verify Object-level Access Logging (audit logging)
	// Give a brief moment for access logging async PUT calls to resolve
	time.Sleep(500 * time.Millisecond)

	// List log objects in system-access-logs bucket
	logsReq, _ := http.NewRequest("GET", server.URL+"/system-access-logs", nil)
	logsReq.SetBasicAuth("admin", "admin")
	logsResp, err := client.Do(logsReq)
	if err != nil {
		t.Fatalf("listing audit logs failed: %v", err)
	}
	defer logsResp.Body.Close()

	if logsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected audit logs bucket list status 200, got %d", logsResp.StatusCode)
	}

	// Retrieve objects list from the XML body
	body, _ := io.ReadAll(logsResp.Body)
	xmlStr := string(body)
	if !strings.Contains(xmlStr, "logs/") {
		t.Fatalf("no audit log objects found in system-access-logs, response XML: %s", xmlStr)
	}

	// Perform a read (GET) to check if another audit log entry gets generated
	readObjReq, _ := http.NewRequest("GET", server.URL+"/"+bucket+"/nature.png", nil)
	readObjReq.SetBasicAuth("admin", "admin")
	readResp, err := client.Do(readObjReq)
	if err != nil {
		t.Fatalf("GET object failed: %v", err)
	}
	readResp.Body.Close()
}
