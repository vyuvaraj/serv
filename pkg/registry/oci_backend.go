package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OCIRegistryStore struct {
	BaseURL  string // e.g. "http://localhost:5001" or registry endpoint
	Username string
	Password string
	Client   *http.Client
}

func NewOCIRegistryStore(baseURL, username, password string) *OCIRegistryStore {
	return &OCIRegistryStore{
		BaseURL:  strings.TrimSuffix(baseURL, "/"),
		Username: username,
		Password: password,
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Map key to repository name and tag
// Keys in S3 look like:
// - "mypkg/metadata.json" -> repo: "mypkg", tag: "metadata"
// - "mypkg/mypkg-1.0.0.tar.gz" -> repo: "mypkg", tag: "v1.0.0"
// - "mypkg/mypkg-1.0.0.tar.gz.sig" -> repo: "mypkg", tag: "v1.0.0.sig"
// - "mypkg/mypkg-1.0.0.tar.gz.provenance" -> repo: "mypkg", tag: "v1.0.0.provenance"
func (o *OCIRegistryStore) resolveRepoTag(key string) (string, string) {
	parts := strings.Split(key, "/")
	if len(parts) < 2 {
		return "default", key
	}
	repo := parts[0]
	filename := parts[len(parts)-1]

	if filename == "metadata.json" {
		return repo, "metadata"
	}

	if strings.HasSuffix(filename, ".provenance") {
		base := strings.TrimSuffix(filename, ".provenance")
		version := o.extractVersion(base, repo)
		return repo, version + ".provenance"
	}

	if strings.HasSuffix(filename, ".sig") {
		base := strings.TrimSuffix(filename, ".sig")
		version := o.extractVersion(base, repo)
		return repo, version + ".sig"
	}

	version := o.extractVersion(filename, repo)
	return repo, version
}

func (o *OCIRegistryStore) extractVersion(filename, repo string) string {
	// e.g. "mypkg-1.0.0.tar.gz" -> "1.0.0"
	base := strings.TrimSuffix(filename, ".tar.gz")
	prefix := repo + "-"
	if strings.HasPrefix(base, prefix) {
		return strings.TrimPrefix(base, prefix)
	}
	return base
}

type OCIManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	MediaType     string           `json:"mediaType,omitempty"`
	Config        OCIDescriptor    `json:"config"`
	Layers        []OCIDescriptor  `json:"layers"`
}

type OCIDescriptor struct {
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
}

func (o *OCIRegistryStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	repo, tag := o.resolveRepoTag(key)

	// 1. Get Manifest
	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/%s", o.BaseURL, repo, tag)
	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}

	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch OCI manifest (%d): %s", resp.StatusCode, string(body))
	}

	var manifest OCIManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("OCI manifest has no layers: %s", key)
	}

	// 2. Get Blob using the layer digest
	digest := manifest.Layers[0].Digest
	blobURL := fmt.Sprintf("%s/v2/%s/blobs/%s", o.BaseURL, repo, digest)
	req, err = http.NewRequestWithContext(ctx, "GET", blobURL, nil)
	if err != nil {
		return nil, err
	}
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}

	resp, err = o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch OCI blob (%d): %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func (o *OCIRegistryStore) PutObject(ctx context.Context, key string, data []byte) error {
	repo, tag := o.resolveRepoTag(key)

	// 1. Upload Blob
	// Start upload session
	uploadURL := fmt.Sprintf("%s/v2/%s/blobs/uploads/", o.BaseURL, repo)
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, nil)
	if err != nil {
		return err
	}
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}

	resp, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to initiate OCI blob upload (%d): %s", resp.StatusCode, string(body))
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("missing Location header in OCI upload response")
	}

	// If location is relative, prefix it with BaseURL
	if !strings.HasPrefix(location, "http://") && !strings.HasPrefix(location, "https://") {
		location = o.BaseURL + location
	}

	// Compute digest
	hash := sha256.Sum256(data)
	digest := fmt.Sprintf("sha256:%x", hash)

	// Upload actual bytes
	uploadURLWithDigest := location
	if strings.Contains(location, "?") {
		uploadURLWithDigest += "&digest=" + digest
	} else {
		uploadURLWithDigest += "?digest=" + digest
	}

	req, err = http.NewRequestWithContext(ctx, "PUT", uploadURLWithDigest, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}

	resp, err = o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload OCI blob bytes (%d): %s", resp.StatusCode, string(body))
	}

	// 2. Push Config (OCI requires a config descriptor, even if empty)
	configData := []byte("{}")
	configHash := sha256.Sum256(configData)
	configDigest := fmt.Sprintf("sha256:%x", configHash)

	// Start config upload session
	req, err = http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/v2/%s/blobs/uploads/", o.BaseURL, repo), nil)
	if err == nil {
		if o.Username != "" {
			req.SetBasicAuth(o.Username, o.Password)
		}
		cResp, cErr := o.Client.Do(req)
		if cErr == nil {
			cLocation := cResp.Header.Get("Location")
			cResp.Body.Close()
			if cLocation != "" {
				if !strings.HasPrefix(cLocation, "http://") && !strings.HasPrefix(cLocation, "https://") {
					cLocation = o.BaseURL + cLocation
				}
				if strings.Contains(cLocation, "?") {
					cLocation += "&digest=" + configDigest
				} else {
					cLocation += "?digest=" + configDigest
				}
				cReq, _ := http.NewRequestWithContext(ctx, "PUT", cLocation, bytes.NewReader(configData))
				if o.Username != "" {
					cReq.SetBasicAuth(o.Username, o.Password)
				}
				if putResp, putErr := o.Client.Do(cReq); putErr == nil {
					putResp.Body.Close()
				}
			}
		}
	}

	// 3. Put Manifest
	manifest := OCIManifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config: OCIDescriptor{
			MediaType: "application/vnd.oci.image.config.v1+json",
			Size:      int64(len(configData)),
			Digest:    configDigest,
		},
		Layers: []OCIDescriptor{
			{
				MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
				Size:      int64(len(data)),
				Digest:    digest,
			},
		},
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	manifestURL := fmt.Sprintf("%s/v2/%s/manifests/%s", o.BaseURL, repo, tag)
	req, err = http.NewRequestWithContext(ctx, "PUT", manifestURL, bytes.NewReader(manifestBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}

	resp, err = o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload OCI manifest (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (o *OCIRegistryStore) ListObjects(ctx context.Context) ([]string, error) {
	// Query /v2/_catalog to get repos (packages)
	req, err := http.NewRequestWithContext(ctx, "GET", o.BaseURL+"/v2/_catalog", nil)
	if err != nil {
		return nil, err
	}
	if o.Username != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		// Fallback to empty if catalog endpoint is not supported/failing
		return nil, nil
	}
	defer resp.Body.Close()

	type Catalog struct {
		Repositories []string `json:"repositories"`
	}
	var cat Catalog
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		return nil, nil
	}

	var keys []string
	for _, repo := range cat.Repositories {
		keys = append(keys, repo+"/metadata.json")
	}
	return keys, nil
}
