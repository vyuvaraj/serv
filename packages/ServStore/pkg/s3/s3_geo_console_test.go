package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestS3GeoAndConsole(t *testing.T) {
	dir, err := os.MkdirTemp("", "servstore-geoconsole-*")
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
	bucket := "geo-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)
	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{}

	// 1. Verify Geo Placement configurations
	geoCfg := storage.GeoPlacementConfig{
		PrimaryRegion:   "us-east-1",
		ReplicaRegions:  []string{"eu-west-1", "ap-south-1"},
		StrictPlacement: true,
	}
	geoBytes, _ := json.Marshal(geoCfg)
	putGeoReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"?geo-placement", bytes.NewReader(geoBytes))
	putGeoReq.Header.Set("Content-Type", "application/json")
	putGeoReq.SetBasicAuth("admin", "admin")

	putGeoResp, err := client.Do(putGeoReq)
	if err != nil {
		t.Fatalf("PUT geo failed: %v", err)
	}
	putGeoResp.Body.Close()
	if putGeoResp.StatusCode != http.StatusOK {
		t.Fatalf("expected PUT geo 200, got %d", putGeoResp.StatusCode)
	}

	getGeoReq, _ := http.NewRequest("GET", server.URL+"/"+bucket+"?geo-placement", nil)
	getGeoReq.SetBasicAuth("admin", "admin")
	getGeoResp, err := client.Do(getGeoReq)
	if err != nil {
		t.Fatalf("GET geo failed: %v", err)
	}
	defer getGeoResp.Body.Close()
	if getGeoResp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET geo 200, got %d", getGeoResp.StatusCode)
	}

	var fetchedGeo storage.GeoPlacementConfig
	if err := json.NewDecoder(getGeoResp.Body).Decode(&fetchedGeo); err != nil {
		t.Fatalf("failed to decode geo: %v", err)
	}
	if fetchedGeo.PrimaryRegion != "us-east-1" || len(fetchedGeo.ReplicaRegions) != 2 {
		t.Fatalf("geo config mismatch: %+v", fetchedGeo)
	}

	// 2. Verify Console login / logout / session validation
	loginPayload := map[string]string{
		"username": "admin",
		"password": "admin",
	}
	loginBytes, _ := json.Marshal(loginPayload)
	loginReq, _ := http.NewRequest("POST", server.URL+"/console/login", bytes.NewReader(loginBytes))
	loginReq.Header.Set("Content-Type", "application/json")

	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login POST failed: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected login 200, got %d", loginResp.StatusCode)
	}

	var session storage.ConsoleSession
	if err := json.NewDecoder(loginResp.Body).Decode(&session); err != nil {
		t.Fatalf("failed to decode session: %v", err)
	}
	if session.Username != "admin" || session.SessionID == "" {
		t.Fatalf("invalid session returned: %+v", session)
	}

	// Verify session validation
	sessReq, _ := http.NewRequest("GET", server.URL+"/console/session", nil)
	sessReq.Header.Set("Authorization", "Bearer "+session.SessionID)
	sessResp, err := client.Do(sessReq)
	if err != nil {
		t.Fatalf("GET session failed: %v", err)
	}
	defer sessResp.Body.Close()
	if sessResp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET session 200, got %d", sessResp.StatusCode)
	}

	// Verify logout
	logoutReq, _ := http.NewRequest("POST", server.URL+"/console/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+session.SessionID)
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout POST failed: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("expected logout 200, got %d", logoutResp.StatusCode)
	}

	// Verify session is now invalid (returns 401)
	sessReq2, _ := http.NewRequest("GET", server.URL+"/console/session", nil)
	sessReq2.Header.Set("Authorization", "Bearer "+session.SessionID)
	sessResp2, err := client.Do(sessReq2)
	if err != nil {
		t.Fatalf("GET invalid session failed: %v", err)
	}
	sessResp2.Body.Close()
	if sessResp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected GET invalid session 401, got %d", sessResp2.StatusCode)
	}
}
