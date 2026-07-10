package web

import (
	"bytes"
	"crypto/ed25519"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"servregistry/pkg/registry"
	"servregistry/pkg/resolution"
	"servregistry/pkg/signing"
)

var (
	WebAssets embed.FS

	MarketplaceMu sync.RWMutex
	Marketplace   = []MarketplaceItem{
		{
			Name:        "auth-token-filter",
			Version:     "1.0.0",
			Type:        "wasm_filter",
			Description: "Standard Bearer Token authentication filter compiled to WASM",
			Publisher:   "Servverse Team",
			URL:         "https://serv.dev/marketplace/auth-token-filter-1.0.0.wasm",
			CreatedAt:   time.Now(),
		},
	}

	SchemasMu  sync.RWMutex
	SchemasMap = make(map[string]string)
)

type MarketplaceItem struct {
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Type        string    `json:"type"` // "template", "wasm_filter", "workflow"
	Description string    `json:"description"`
	Publisher   string    `json:"publisher"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
}

func SetWebAssets(fs embed.FS) {
	WebAssets = fs
}

func InitSchemas() {
	os.MkdirAll("schemas", 0755)
	files, err := os.ReadDir("schemas")
	if err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
				name := strings.TrimSuffix(f.Name(), ".json")
				data, err := os.ReadFile(filepath.Join("schemas", f.Name()))
				if err == nil {
					SchemasMap[name] = string(data)
				}
			}
		}
	}
}

func HandlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	// 1. Enforce JWT validation if SERV_JWT_SECRET is set
	if jwtSecret := os.Getenv("SERV_JWT_SECRET"); jwtSecret != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			registry.WriteJSONError(w, r, "Unauthorized: Missing or invalid token", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if _, ok := signing.ValidateJWT(token, []byte(jwtSecret)); !ok {
			registry.WriteJSONError(w, r, "Unauthorized: Invalid JWT", "ERR_INVALID_JWT", http.StatusUnauthorized)
			return
		}
	}

	// Read body in memory
	data, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Failed to read body: %v", err)
		registry.WriteJSONError(w, r, "Failed to read request body", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}

	// Cryptographic Ed25519 signature check
	sigHex := r.Header.Get("X-Signature")
	if sigHex == "" {
		sigHex = r.Header.Get("signature")
	}
	pubKeyHex := r.Header.Get("X-Public-Key")
	if pubKeyHex == "" {
		pubKeyHex = r.Header.Get("public-key")
	}

	if sigHex == "" || pubKeyHex == "" {
		registry.WriteJSONError(w, r, "Missing signature or public key", "ERR_MISSING_SIGNATURE", http.StatusBadRequest)
		return
	}

	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		registry.WriteJSONError(w, r, "Invalid signature format", "ERR_INVALID_SIGNATURE", http.StatusBadRequest)
		return
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		registry.WriteJSONError(w, r, "Invalid public key format", "ERR_INVALID_PUBLIC_KEY", http.StatusBadRequest)
		return
	}

	if !ed25519.Verify(pubKeyBytes, data, sigBytes) {
		registry.WriteJSONError(w, r, "Signature verification failed", "ERR_SIGNATURE_VERIFICATION_FAILED", http.StatusBadRequest)
		return
	}

	// 2. Parse serv.toml from uploaded tar.gz
	name, version, deps, err := resolution.ParseServTomlFromTarGz(data)
	if err != nil {
		// Fallback to query parameter name & default version
		log.Printf("Manifest parsing failed or not found: %v. Using fallback", err)
		name = r.URL.Query().Get("name")
		if name == "" {
			registry.WriteJSONError(w, r, "Missing 'name' parameter and no serv.toml found", "ERR_MISSING_NAME_PARAMETER", http.StatusBadRequest)
			return
		}
		version = r.URL.Query().Get("version")
		if version == "" {
			version = "0.0.0"
		}
		deps = []string{}
	}

	// Sanitize name
	var safeName string
	if strings.HasPrefix(name, "@") && strings.Count(name, "/") == 1 {
		parts := strings.Split(name, "/")
		org := parts[0]
		pkg := parts[1]
		safeName = org + "/" + strings.TrimSpace(filepath.Base(pkg))
	} else {
		safeName = strings.TrimSpace(filepath.Base(name))
	}
	name = safeName
	version = strings.TrimSpace(filepath.Base(version))
	if name == "" || name == "." || name == "/" || version == "" {
		registry.WriteJSONError(w, r, "Invalid package name or version", "ERR_INVALID_PACKAGE_VERSION", http.StatusBadRequest)
		return
	}

	// Verify ACL authorization
	if !registry.AclStore.Authorize(name, pubKeyHex) {
		registry.WriteJSONError(w, r, "Forbidden: Namespace or Package owned by another publisher", "ERR_FORBIDDEN", http.StatusForbidden)
		return
	}

	log.Printf("Publishing package: %s @ %s", name, version)

	// 3. Update metadata.json on S3
	metadataKey := fmt.Sprintf("%s/metadata.json", name)
	var metadata registry.PackageMetadata
	metadata.Name = name
	metadata.Versions = make(map[string]registry.VersionDetails)

	// Try to load existing metadata.json
	metaResp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(metadataKey),
	})
	if err == nil {
		metaData, merr := io.ReadAll(metaResp.Body)
		metaResp.Body.Close()
		if merr == nil {
			_ = json.Unmarshal(metaData, &metadata)
		}
	}

	// Add/update version details
	metadata.Versions[version] = registry.VersionDetails{
		Version:      version,
		Dependencies: deps,
		Size:         int64(len(data)),
		PublishedAt:  time.Now().Format(time.RFC3339),
	}

	updatedMetaBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		registry.WriteJSONError(w, r, "Failed to serialize metadata", "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	// Upload updated metadata.json
	_, err = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(registry.BucketName),
		Key:         aws.String(metadataKey),
		Body:        bytes.NewReader(updatedMetaBytes),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		log.Printf("Failed to upload metadata to S3: %v", err)
		registry.WriteJSONError(w, r, "Failed to upload metadata", "ERR_METADATA_UPLOAD_FAILED", http.StatusInternalServerError)
		return
	}

	// 4. Upload tarball payload to S3 key structure: {name}/{version}/{name}-{version}.tar.gz
	safeFilename := strings.ReplaceAll(name, "/", "-")
	objectKey := fmt.Sprintf("%s/%s/%s-%s.tar.gz", name, version, safeFilename, version)
	_, err = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(registry.BucketName),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		log.Printf("Failed to upload package tarball to S3: %v", err)
		registry.WriteJSONError(w, r, "Failed to upload package to storage: "+err.Error(), "ERR_PACKAGE_UPLOAD_FAILED", http.StatusInternalServerError)
		return
	}

	// 5. Upload signature companion to S3
	sigObjectKey := fmt.Sprintf("%s/%s/%s-%s.tar.gz.sig", name, version, safeFilename, version)
	_, err = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(registry.BucketName),
		Key:         aws.String(sigObjectKey),
		Body:        bytes.NewReader(sigBytes),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		log.Printf("Failed to upload signature companion to S3: %v", err)
		registry.WriteJSONError(w, r, "Failed to upload signature to storage: "+err.Error(), "ERR_SIGNATURE_UPLOAD_FAILED", http.StatusInternalServerError)
		return
	}

	// 5.1 Upload build provenance if present
	var provenanceData []byte
	if provBase64 := r.Header.Get("X-Provenance"); provBase64 != "" {
		if provBytes, err := base64.StdEncoding.DecodeString(provBase64); err == nil {
			provenanceData = provBytes
		}
	} else if provHeader := r.Header.Get("provenance"); provHeader != "" {
		provenanceData = []byte(provHeader)
	} else if provCommit := r.Header.Get("X-Provenance-Commit"); provCommit != "" {
		provMap := map[string]string{
			"commit":     provCommit,
			"ci_run_id":  r.Header.Get("X-Provenance-CI-Run"),
			"builder":    r.Header.Get("X-Provenance-Builder"),
			"created_at": time.Now().Format(time.RFC3339),
		}
		provenanceData, _ = json.Marshal(provMap)
	}

	if len(provenanceData) > 0 {
		provKey := fmt.Sprintf("%s/%s/provenance.json", name, version)
		_, _ = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
			Bucket:      aws.String(registry.BucketName),
			Key:         aws.String(provKey),
			Body:        bytes.NewReader(provenanceData),
			ContentType: aws.String("application/json"),
		})
		log.Printf("Recorded build provenance attestation for %s @ %s", name, version)
	}

	// Proactively update local cache index
	registry.PackageIndexMu.Lock()
	versions := []string{}
	var latest string
	var latestTime time.Time
	for v, details := range metadata.Versions {
		versions = append(versions, v)
		t, err := time.Parse(time.RFC3339, details.PublishedAt)
		if err == nil && t.After(latestTime) {
			latestTime = t
			latest = v
		}
	}
	registry.PackageIndex[name] = &registry.PackageIndexItem{
		Name:         name,
		Latest:       latest,
		Versions:     versions,
		LastModified: latestTime,
	}
	registry.PackageIndexMu.Unlock()

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "✓ Package '%s' @ '%s' successfully published to registry!\n", name, version)
}

func HandleGetPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	// Path will be "/packages/{name}.tar.gz" or "/packages/{name}/{version}/{name}-{version}.tar.gz"
	var path string
	if strings.HasPrefix(r.URL.Path, "/api/v1/packages/") {
		path = strings.TrimPrefix(r.URL.Path, "/api/v1/packages/")
	} else {
		path = strings.TrimPrefix(r.URL.Path, "/packages/")
	}
	if path == "" {
		registry.WriteJSONError(w, r, "Missing package filename", "ERR_MISSING_FILENAME", http.StatusBadRequest)
		return
	}

	log.Printf("Fetching package: %s", path)

	var s3Key string
	var name, version string

	parts := strings.Split(path, "/")
	if len(parts) == 4 && strings.HasPrefix(parts[0], "@") {
		name = parts[0] + "/" + parts[1]
		version = parts[2]
		if strings.HasPrefix(version, "^") || strings.HasPrefix(version, "~") {
			metadataKey := fmt.Sprintf("%s/metadata.json", name)
			metaResp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
				Bucket: aws.String(registry.BucketName),
				Key:    aws.String(metadataKey),
			})
			if err == nil {
				defer metaResp.Body.Close()
				var metadata registry.PackageMetadata
				metaData, merr := io.ReadAll(metaResp.Body)
				if merr == nil && json.Unmarshal(metaData, &metadata) == nil {
					version = resolution.ResolveBestVersion(version, metadata.Versions)
				}
			}
		}
		safeFilename := strings.ReplaceAll(name, "/", "-")
		s3Key = fmt.Sprintf("%s/%s/%s-%s.tar.gz", name, version, safeFilename, version)
	} else if len(parts) == 3 {
		name = parts[0]
		version = parts[1]
		if strings.HasPrefix(version, "^") || strings.HasPrefix(version, "~") {
			metadataKey := fmt.Sprintf("%s/metadata.json", name)
			metaResp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
				Bucket: aws.String(registry.BucketName),
				Key:    aws.String(metadataKey),
			})
			if err == nil {
				defer metaResp.Body.Close()
				var metadata registry.PackageMetadata
				metaData, merr := io.ReadAll(metaResp.Body)
				if merr == nil && json.Unmarshal(metaData, &metadata) == nil {
					version = resolution.ResolveBestVersion(version, metadata.Versions)
				}
			}
		}
		s3Key = fmt.Sprintf("%s/%s/%s-%s.tar.gz", name, version, name, version)
	} else if !strings.Contains(path, "/") && strings.HasSuffix(path, ".tar.gz") {
		name = strings.TrimSuffix(path, ".tar.gz")
		metadataKey := fmt.Sprintf("%s/metadata.json", name)
		metaResp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
			Bucket: aws.String(registry.BucketName),
			Key:    aws.String(metadataKey),
		})
		if err != nil {
			s3Key = path
		} else {
			defer metaResp.Body.Close()
			var metadata registry.PackageMetadata
			metaData, merr := io.ReadAll(metaResp.Body)
			if merr == nil && json.Unmarshal(metaData, &metadata) == nil && len(metadata.Versions) > 0 {
				var latest string
				var latestTime time.Time
				for v, details := range metadata.Versions {
					t, err := time.Parse(time.RFC3339, details.PublishedAt)
					if err == nil && t.After(latestTime) {
						latestTime = t
						latest = v
					} else if latest == "" {
						latest = v
					}
				}
				version = latest
				s3Key = fmt.Sprintf("%s/%s/%s-%s.tar.gz", name, latest, name, latest)
			} else {
				s3Key = path
			}
		}
	} else {
		s3Key = path
		if len(parts) >= 2 {
			if strings.HasPrefix(parts[0], "@") && len(parts) >= 3 {
				name = parts[0] + "/" + parts[1]
				version = parts[2]
			} else {
				name = parts[0]
				version = parts[1]
			}
		}
	}

	resp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		upstream := os.Getenv("SERV_UPSTREAM_REGISTRY")
		if upstream != "" {
			upstream = strings.TrimSuffix(upstream, "/")
			upstreamURL := fmt.Sprintf("%s/packages/%s", upstream, path)
			log.Printf("Local package not found, proxying to upstream: %s", upstreamURL)

			upReq, _ := http.NewRequestWithContext(r.Context(), "GET", upstreamURL, nil)
			client := &http.Client{Timeout: 15 * time.Second}
			upResp, err := client.Do(upReq)
			if err == nil && upResp.StatusCode == http.StatusOK {
				defer upResp.Body.Close()
				data, readErr := io.ReadAll(upResp.Body)
				if readErr == nil {
					// Cache local S3
					_, _ = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
						Bucket:      aws.String(registry.BucketName),
						Key:         aws.String(s3Key),
						Body:        bytes.NewReader(data),
						ContentType: aws.String("application/octet-stream"),
					})
					log.Printf("Successfully cached package %s from upstream to local S3", s3Key)

					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
					w.Write(data)
					return
				}
			}
			if err == nil {
				upResp.Body.Close()
			}
		}

		log.Printf("Failed to get object from S3: %v", err)
		registry.WriteJSONError(w, r, "Package not found", "ERR_PACKAGE_NOT_FOUND", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	if name != "" && version != "" {
		registry.CheckDeprecationsAndAddHeader(w, r.Context(), name, version)
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Error copying package body: %v", err)
	}
}

func HandleSearchPackages(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(r.URL.Query().Get("q"))

	registry.PackageIndexMu.RLock()
	results := []*registry.PackageIndexItem{}
	for name, item := range registry.PackageIndex {
		if query == "" || strings.Contains(strings.ToLower(name), query) {
			results = append(results, item)
		}
	}
	registry.PackageIndexMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func HandlePackagesAPI(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/versions") {
		handleGetVersions(w, r)
		return
	}
	if strings.Contains(r.URL.Path, "/deps") {
		handleGetDeps(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/deprecate") {
		handleDeprecate(w, r)
		return
	}
	handleListPackages(w, r)
}

func handleListPackages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	registry.PackageIndexMu.RLock()
	packages := make([]registry.PackageIndexItem, 0, len(registry.PackageIndex))
	for _, item := range registry.PackageIndex {
		packages = append(packages, *item)
	}
	registry.PackageIndexMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(packages)
}

func handleGetVersions(w http.ResponseWriter, r *http.Request) {
	var path string
	if strings.HasPrefix(r.URL.Path, "/api/v1/packages/") {
		path = strings.TrimPrefix(r.URL.Path, "/api/v1/packages/")
	} else {
		path = strings.TrimPrefix(r.URL.Path, "/api/packages/")
	}
	path = strings.TrimSuffix(path, "/versions")
	name := strings.TrimSpace(path)
	if name == "" {
		registry.WriteJSONError(w, r, "Missing package name", "ERR_MISSING_NAME_PARAMETER", http.StatusBadRequest)
		return
	}

	metadataKey := fmt.Sprintf("%s/metadata.json", name)
	resp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(metadataKey),
	})
	if err != nil {
		registry.WriteJSONError(w, r, "Package not found", "ERR_PACKAGE_NOT_FOUND", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func handleGetDeps(w http.ResponseWriter, r *http.Request) {
	var path string
	if strings.HasPrefix(r.URL.Path, "/api/v1/packages/") {
		path = strings.TrimPrefix(r.URL.Path, "/api/v1/packages/")
	} else {
		path = strings.TrimPrefix(r.URL.Path, "/api/packages/")
	}
	path = strings.TrimSuffix(path, "/deps")
	parts := strings.Split(path, "/")

	var name, version string
	if len(parts) >= 2 && strings.HasPrefix(parts[0], "@") {
		name = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			version = parts[2]
		}
	} else if len(parts) == 1 {
		name = parts[0]
	} else if len(parts) == 2 {
		name = parts[0]
		version = parts[1]
	} else {
		registry.WriteJSONError(w, r, "Invalid path", "ERR_INVALID_PATH", http.StatusBadRequest)
		return
	}

	// Fetch metadata
	metadataKey := fmt.Sprintf("%s/metadata.json", name)
	resp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(metadataKey),
	})
	if err != nil {
		registry.WriteJSONError(w, r, "Package not found", "ERR_PACKAGE_NOT_FOUND", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		registry.WriteJSONError(w, r, "Failed to read metadata", "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	var metadata registry.PackageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		registry.WriteJSONError(w, r, "Failed to parse metadata", "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	// If no version specified, use latest
	if version == "" {
		var latestTime time.Time
		for v, details := range metadata.Versions {
			t, err := time.Parse(time.RFC3339, details.PublishedAt)
			if err == nil && t.After(latestTime) {
				latestTime = t
				version = v
			}
		}
	} else if strings.HasPrefix(version, "^") || strings.HasPrefix(version, "~") {
		version = resolution.ResolveBestVersion(version, metadata.Versions)
	}

	versionDetails, ok := metadata.Versions[version]
	if !ok {
		registry.WriteJSONError(w, r, fmt.Sprintf("Version %s not found", version), "ERR_VERSION_NOT_FOUND", http.StatusNotFound)
		return
	}

	// Resolve full dependency tree (BFS)
	type DepNode struct {
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Dependencies []string `json:"dependencies"`
	}

	resolved := []DepNode{{
		Name:         name,
		Version:      version,
		Dependencies: versionDetails.Dependencies,
	}}

	seen := map[string]bool{name: true}
	queue := versionDetails.Dependencies

	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]

		// Parse "pkgname@version"
		depParts := strings.SplitN(dep, "@", 2)
		depName := depParts[0]
		if seen[depName] {
			continue
		}
		seen[depName] = true

		depMetaKey := fmt.Sprintf("%s/metadata.json", depName)
		depResp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
			Bucket: aws.String(registry.BucketName),
			Key:    aws.String(depMetaKey),
		})
		if err != nil {
			resolved = append(resolved, DepNode{Name: depName, Version: "unknown", Dependencies: nil})
			continue
		}

		depData, _ := io.ReadAll(depResp.Body)
		depResp.Body.Close()

		var depMeta registry.PackageMetadata
		if err := json.Unmarshal(depData, &depMeta); err != nil {
			continue
		}

		// Resolve version: use requested version or latest
		depVersion := ""
		if len(depParts) == 2 {
			depVersion = depParts[1]
		}
		if depVersion == "" || depMeta.Versions[depVersion].Version == "" {
			var latestTime time.Time
			for v, details := range depMeta.Versions {
				t, err := time.Parse(time.RFC3339, details.PublishedAt)
				if err == nil && t.After(latestTime) {
					latestTime = t
					depVersion = v
				}
			}
		}

		if vd, ok := depMeta.Versions[depVersion]; ok {
			resolved = append(resolved, DepNode{Name: depName, Version: depVersion, Dependencies: vd.Dependencies})
			queue = append(queue, vd.Dependencies...)
		} else {
			resolved = append(resolved, DepNode{Name: depName, Version: depVersion, Dependencies: nil})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"package":  name,
		"version":  version,
		"tree":     resolved,
		"resolved": len(resolved),
	})
}

func handleDeprecate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	if jwtSecret := os.Getenv("SERV_JWT_SECRET"); jwtSecret != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			registry.WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if _, ok := signing.ValidateJWT(token, []byte(jwtSecret)); !ok {
			registry.WriteJSONError(w, r, "Unauthorized", "ERR_INVALID_JWT", http.StatusUnauthorized)
			return
		}
	}

	var path string
	if strings.HasPrefix(r.URL.Path, "/api/v1/packages/") {
		path = strings.TrimPrefix(r.URL.Path, "/api/v1/packages/")
	} else {
		path = strings.TrimPrefix(r.URL.Path, "/api/packages/")
	}
	path = strings.TrimSuffix(path, "/deprecate")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		registry.WriteJSONError(w, r, "Invalid path", "ERR_INVALID_PATH", http.StatusBadRequest)
		return
	}
	name, version := parts[0], parts[1]

	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	metadataKey := fmt.Sprintf("%s/metadata.json", name)
	resp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(metadataKey),
	})
	if err != nil {
		registry.WriteJSONError(w, r, "Package not found", "ERR_PACKAGE_NOT_FOUND", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	metaData, _ := io.ReadAll(resp.Body)
	var metadata registry.PackageMetadata
	_ = json.Unmarshal(metaData, &metadata)

	vd, ok := metadata.Versions[version]
	if !ok {
		registry.WriteJSONError(w, r, "Version not found", "ERR_VERSION_NOT_FOUND", http.StatusNotFound)
		return
	}

	vd.Deprecated = true
	vd.DeprecationMsg = body.Message
	metadata.Versions[version] = vd

	updatedMetaBytes, _ := json.MarshalIndent(metadata, "", "  ")
	_, err = registry.S3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(registry.BucketName),
		Key:         aws.String(metadataKey),
		Body:        bytes.NewReader(updatedMetaBytes),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		registry.WriteJSONError(w, r, "Failed to save deprecation", "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success":true,"message":"Version deprecated successfully"}`))
}

func HandleSchemasAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		registry.WriteJSONError(w, r, "Invalid path", "ERR_INVALID_PATH", http.StatusBadRequest)
		return
	}
	name := parts[4]
	if name == "" {
		registry.WriteJSONError(w, r, "Schema name required", "ERR_NAME_REQUIRED", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			registry.WriteJSONError(w, r, "Failed to read body", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}

		var js map[string]interface{}
		if err := json.Unmarshal(body, &js); err != nil {
			registry.WriteJSONError(w, r, "Invalid schema JSON", "ERR_INVALID_SCHEMA", http.StatusBadRequest)
			return
		}

		SchemasMu.Lock()
		SchemasMap[name] = string(body)
		SchemasMu.Unlock()

		_ = os.WriteFile(filepath.Join("schemas", name+".json"), body, 0644)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))

	case http.MethodGet:
		SchemasMu.RLock()
		schema, exists := SchemasMap[name]
		SchemasMu.RUnlock()

		if !exists {
			registry.WriteJSONError(w, r, "Schema not found", "ERR_NOT_FOUND", http.StatusNotFound)
			return
		}
		w.Write([]byte(schema))

	default:
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
	}
}

func HandleSchemaValidationAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SchemaName string `json:"schema"`
		Payload    string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		registry.WriteJSONError(w, r, "Invalid request payload", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	SchemasMu.RLock()
	schemaStr, exists := SchemasMap[req.SchemaName]
	SchemasMu.RUnlock()

	if !exists {
		registry.WriteJSONError(w, r, "Schema not found", "ERR_SCHEMA_NOT_FOUND", http.StatusNotFound)
		return
	}

	var schemaObj, payloadObj map[string]interface{}
	_ = json.Unmarshal([]byte(schemaStr), &schemaObj)

	if err := json.Unmarshal([]byte(req.Payload), &payloadObj); err != nil {
		w.Write([]byte(`{"valid":false,"errors":["Invalid payload JSON"]}`))
		return
	}

	var validationErrors []string
	if props, ok := schemaObj["properties"].(map[string]interface{}); ok {
		for key, propVal := range props {
			if propMap, ok := propVal.(map[string]interface{}); ok {
				expectedType, _ := propMap["type"].(string)
				val, exists := payloadObj[key]
				if !exists {
					if reqList, ok := schemaObj["required"].([]interface{}); ok {
						for _, rKey := range reqList {
							if rKey == key {
								validationErrors = append(validationErrors, fmt.Sprintf("Missing required property: %s", key))
							}
						}
					}
					continue
				}

				switch expectedType {
				case "string":
					if _, ok := val.(string); !ok {
						validationErrors = append(validationErrors, fmt.Sprintf("Property %s must be a string", key))
					}
				case "number", "integer":
					if _, ok := val.(float64); !ok {
						validationErrors = append(validationErrors, fmt.Sprintf("Property %s must be a number", key))
					}
				case "boolean":
					if _, ok := val.(bool); !ok {
						validationErrors = append(validationErrors, fmt.Sprintf("Property %s must be a boolean", key))
					}
				}
			}
		}
	}

	if len(validationErrors) > 0 {
		resp, _ := json.Marshal(map[string]interface{}{
			"valid":  false,
			"errors": validationErrors,
		})
		w.Write(resp)
	} else {
		w.Write([]byte(`{"valid":true}`))
	}
}

func HandleMarketplaceList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	MarketplaceMu.RLock()
	defer MarketplaceMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Marketplace)
}

func HandleMarketplacePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var item MarketplaceItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if item.Name == "" || item.Version == "" || item.Type == "" {
		http.Error(w, "name, version, and type are required", http.StatusBadRequest)
		return
	}
	item.CreatedAt = time.Now()

	MarketplaceMu.Lock()
	Marketplace = append(Marketplace, item)
	MarketplaceMu.Unlock()

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"status":"published"}`))
}

func HandleGetProvenance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		registry.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/packages/provenance/")
	parts := strings.Split(path, "/")

	var name, version string
	if len(parts) == 3 && strings.HasPrefix(parts[0], "@") {
		name = parts[0] + "/" + parts[1]
		version = parts[2]
	} else if len(parts) == 2 {
		name = parts[0]
		version = parts[1]
	} else {
		registry.WriteJSONError(w, r, "Invalid package name or version path", "ERR_INVALID_PATH", http.StatusBadRequest)
		return
	}

	provKey := fmt.Sprintf("%s/%s/provenance.json", name, version)
	resp, err := registry.S3Client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(registry.BucketName),
		Key:    aws.String(provKey),
	})
	if err != nil {
		registry.WriteJSONError(w, r, "Provenance attestation not found", "ERR_PROVENANCE_NOT_FOUND", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func HandleWebDashboard(w http.ResponseWriter, r *http.Request) {
	// Serve embedded dashboard static files
	path := r.URL.Path
	if path == "/" {
		path = "/web/index.html"
	} else {
		path = "/web" + path
	}

	// Try reading file from embedded fs
	data, err := WebAssets.ReadFile(strings.TrimPrefix(path, "/"))
	if err != nil {
		// Fallback to index.html for single page app routing or 404
		data, err = WebAssets.ReadFile("web/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		path = "/web/index.html"
	}

	// Set content type
	switch {
	case strings.HasSuffix(path, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}

	w.Write(data)
}
