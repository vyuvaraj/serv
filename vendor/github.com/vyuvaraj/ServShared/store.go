package ServShared

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// StoreClient handles communication with ServStore (S3-compatible distributed storage).
type StoreClient struct {
	Endpoint  string
	AuthToken string
}

// NewStoreClient returns a new client using environment variables.
func NewStoreClient() *StoreClient {
	endpoint := os.Getenv("SERV_STORE_URL")
	if endpoint == "" {
		endpoint = "http://localhost:8081"
	}
	authToken := os.Getenv("SERV_STORE_TOKEN")
	if authToken == "" {
		authToken = "gateway-secret-token"
	}
	return &StoreClient{
		Endpoint:  strings.TrimSuffix(endpoint, "/"),
		AuthToken: authToken,
	}
}

// EnsureBucket creates a bucket in ServStore if it does not already exist.
func (c *StoreClient) EnsureBucket(bucket string) error {
	url := fmt.Sprintf("%s/%s", c.Endpoint, bucket)
	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		return err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Put writes object data to a bucket and key in ServStore.
func (c *StoreClient) Put(bucket, key string, data []byte) error {
	_ = c.EnsureBucket(bucket) // Ensure bucket exists first
	url := fmt.Sprintf("%s/%s/%s", c.Endpoint, bucket, key)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to save to ServStore (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// Get reads object data from a bucket and key in ServStore.
func (c *StoreClient) Get(bucket, key string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/%s", c.Endpoint, bucket, key)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to read from ServStore (%d): %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}
