package s3

import (
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestS3SelectCSV(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}

	ctx := t.Context()
	bucket := "test-select"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		// Close store before failing to avoid TempDir cleanup race
		store.Close()
		t.Fatalf("failed to create bucket: %v", err)
	}

	csvData := "name,age,city\nAlice,30,New York\nBob,25,San Francisco\nCharlie,35,Los Angeles\n"
	_, err = store.PutObject(ctx, bucket, "users.csv", strings.NewReader(csvData), int64(len(csvData)), "text/csv")
	if err != nil {
		store.Close()
		t.Fatalf("failed to put csv: %v", err)
	}

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)
	server := httptest.NewServer(gateway)

	// Ensure shutdown order: server first (drains in-flight requests),
	// then store (flushes Pebble WAL/SST), both before TempDir RemoveAll.
	// t.Cleanup runs LIFO, so register store cleanup first so it runs last.
	t.Cleanup(func() { store.Close() })
	t.Cleanup(func() { server.Close() })

	client := &http.Client{}

	// Test 1: Query with headers
	reqBody := `<?xml version="1.0" encoding="UTF-8"?>
<SelectObjectContentRequest>
    <Expression>SELECT s.name, s.age FROM s3object s WHERE s.age = '30'</Expression>
    <ExpressionType>SQL</ExpressionType>
    <InputSerialization>
        <CSV>
            <FileHeaderInfo>USE</FileHeaderInfo>
        </CSV>
    </InputSerialization>
    <OutputSerialization>
        <CSV/>
    </OutputSerialization>
</SelectObjectContentRequest>`

	req, _ := http.NewRequest("POST", server.URL+"/"+bucket+"/users.csv?select", strings.NewReader(reqBody))
	req.SetBasicAuth("admin", "admin")
	req.Header.Set("Content-Type", "application/xml")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Read and verify the S3 select event stream
	results := readEventStream(t, resp.Body)
	expected := "Alice,30\n"
	if !strings.Contains(results, expected) {
		t.Fatalf("expected output to contain %q, got: %q", expected, results)
	}
}

func readEventStream(t *testing.T, r io.Reader) string {
	var recordsBuilder strings.Builder

	for {
		preamble := make([]byte, 12)
		_, err := io.ReadFull(r, preamble)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("failed to read preamble: %v", err)
		}

		totalLen := binary.BigEndian.Uint32(preamble[0:4])
		headersLen := binary.BigEndian.Uint32(preamble[4:8])

		headersBuf := make([]byte, headersLen)
		_, err = io.ReadFull(r, headersBuf)
		if err != nil {
			t.Fatalf("failed to read headers: %v", err)
		}

		payloadLen := totalLen - 12 - headersLen - 4
		payloadBuf := make([]byte, payloadLen)
		_, err = io.ReadFull(r, payloadBuf)
		if err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}

		// Read CRC
		crcBuf := make([]byte, 4)
		_, err = io.ReadFull(r, crcBuf)
		if err != nil {
			t.Fatalf("failed to read CRC: %v", err)
		}

		// Extract event-type header
		eventType := extractHeader(headersBuf, ":event-type")
		if eventType == "Records" {
			recordsBuilder.Write(payloadBuf)
		} else if eventType == "End" {
			break
		}
	}

	return recordsBuilder.String()
}

func extractHeader(headers []byte, name string) string {
	idx := 0
	for idx < len(headers) {
		nameLen := int(headers[idx])
		idx++
		headerName := string(headers[idx : idx+nameLen])
		idx += nameLen

		headerType := headers[idx]
		idx++

		if headerType == 7 { // Type string
			valLen := int(binary.BigEndian.Uint16(headers[idx : idx+2]))
			idx += 2
			val := string(headers[idx : idx+valLen])
			idx += valLen

			if headerName == name {
				return val
			}
		}
	}
	return ""
}
