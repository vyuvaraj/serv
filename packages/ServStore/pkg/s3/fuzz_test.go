package s3

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func FuzzS3GatewayRequests(f *testing.F) {
	// Setup seeds representing S3 APIs (HTTP verb, path, query, header key, header val, body payload)
	f.Add("GET", "/testbucket", "location", "Authorization", "AWS4-HMAC-SHA256...", []byte{})
	f.Add("PUT", "/testbucket", "versioning", "Content-Type", "application/xml", []byte(`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`))
	f.Add("POST", "/testbucket/myobject", "uploads", "X-ServStore-Namespace", "tenant-1", []byte{})
	f.Add("POST", "/testbucket/myobject", "transform=true&target-key=other&mem-limit=64&timeout=10", "Content-Type", "application/octet-stream", []byte{1, 2, 3})
	f.Add("DELETE", "/testbucket/myobject", "versionId=123", "Authorization", "bad", []byte{})

	f.Fuzz(func(t *testing.T, method string, path string, query string, headerKey string, headerVal string, body []byte) {
		// Create temporary local storage engine for this run
		tempDir, err := os.MkdirTemp("", "servstore-fuzz-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(tempDir)

		store, err := storage.NewLocalStore(tempDir)
		if err != nil {
			return
		}

		// Disable Auth checks inside testing fuzz to ensure all deeper XML parsing,
		// routing, parameter unpacking, and inner code blocks are fully reached.
		authProvider := auth.NewAuthProvider("minioadmin", "minioadmin", false)
		gateway := NewGateway(store, authProvider, nil, nil, 2, false, 2, 1)

		// Sanitize request URI path (prevent invalid path parsing crashes before ServeHTTP)
		if path == "" || path[0] != '/' {
			path = "/" + path
		}

		reqUrl := path
		if query != "" {
			reqUrl = reqUrl + "?" + query
		}

		req, err := http.NewRequest(method, reqUrl, bytes.NewReader(body))
		if err != nil {
			return
		}

		// Inject fuzzed headers
		if headerKey != "" && headerVal != "" {
			req.Header.Set(headerKey, headerVal)
		}

		// Record the response
		rec := httptest.NewRecorder()

		// Run ServeHTTP. Defend against panics (asserting recovery and no crashes).
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic caught: %v\nMethod: %s, URL: %s, BodyLen: %d", r, method, reqUrl, len(body))
			}
		}()

		gateway.ServeHTTP(rec, req)
	})
}
