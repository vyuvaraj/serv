package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type PackageIndexItem struct {
	Name         string    `json:"name"`
	Latest       string    `json:"latest"`
	Versions     []string  `json:"versions"`
	LastModified time.Time `json:"lastModified"`
}

type PackageMetadata struct {
	Name           string                    `json:"name"`
	Versions       map[string]VersionDetails `json:"versions"`
	Deprecated     bool                      `json:"deprecated,omitempty"`
	DeprecationMsg string                    `json:"deprecationMsg,omitempty"`
}

type VersionDetails struct {
	Version        string   `json:"version"`
	Dependencies   []string `json:"dependencies"`
	Size           int64    `json:"size"`
	PublishedAt    string   `json:"publishedAt"`
	Deprecated     bool     `json:"deprecated,omitempty"`
	DeprecationMsg string   `json:"deprecationMsg,omitempty"`
}

type PackageInfo struct {
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
}

var (
	S3Client   *s3.Client
	BucketName = "serv-packages"

	PackageIndexMu sync.RWMutex
	PackageIndex   = make(map[string]*PackageIndexItem)
)

type ACLStore struct {
	mu       sync.Mutex
	filePath string
	Owners   map[string]string `json:"owners"` // package name or scope -> public key hex
}

func NewACLStore(filePath string) *ACLStore {
	store := &ACLStore{
		filePath: filePath,
		Owners:   make(map[string]string),
	}
	store.Load()
	return store
}

func (s *ACLStore) Load() {
	data, err := os.ReadFile(s.filePath)
	if err == nil {
		_ = json.Unmarshal(data, &s.Owners)
	}
}

func (s *ACLStore) Save() {
	data, err := json.MarshalIndent(s.Owners, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.filePath, data, 0644)
	}
}

func (s *ACLStore) Authorize(packageName, pubKeyHex string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyToCheck := packageName
	if strings.HasPrefix(packageName, "@") && strings.Contains(packageName, "/") {
		keyToCheck = strings.Split(packageName, "/")[0]
	}

	owner, exists := s.Owners[keyToCheck]
	if !exists {
		s.Owners[keyToCheck] = pubKeyHex
		s.Save()
		return true
	}

	return owner == pubKeyHex
}

var AclStore *ACLStore

func EnsureBucketExists(ctx context.Context) {
	_, err := S3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(BucketName),
	})
	if err == nil {
		log.Printf("Bucket '%s' verified", BucketName)
		return
	}

	log.Printf("Bucket '%s' does not exist. Creating it...", BucketName)
	_, err = S3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(BucketName),
	})
	if err != nil {
		log.Fatalf("Failed to create bucket '%s': %v", BucketName, err)
	}
	log.Printf("Bucket '%s' successfully created", BucketName)
}

func BuildPackageIndex(ctx context.Context) {
	resp, err := S3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(BucketName),
	})
	if err != nil {
		log.Printf("Failed to list objects for indexing: %v", err)
		return
	}

	PackageIndexMu.Lock()
	defer PackageIndexMu.Unlock()

	PackageIndex = make(map[string]*PackageIndexItem)

	for _, obj := range resp.Contents {
		key := *obj.Key
		if strings.HasSuffix(key, "/metadata.json") {
			mResp, err := S3Client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(BucketName),
				Key:    aws.String(key),
			})
			if err != nil {
				continue
			}
			data, err := io.ReadAll(mResp.Body)
			mResp.Body.Close()
			if err != nil {
				continue
			}

			var meta PackageMetadata
			if err := json.Unmarshal(data, &meta); err == nil {
				versions := []string{}
				var latest string
				var latestTime time.Time
				for v, details := range meta.Versions {
					versions = append(versions, v)
					t, err := time.Parse(time.RFC3339, details.PublishedAt)
					if err == nil && t.After(latestTime) {
						latestTime = t
						latest = v
					} else if latest == "" {
						latest = v
					}
				}
				PackageIndex[meta.Name] = &PackageIndexItem{
					Name:         meta.Name,
					Latest:       latest,
					Versions:     versions,
					LastModified: latestTime,
				}
			}
		}
	}
	log.Printf("Package index built: %d packages found", len(PackageIndex))
}

type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

func WriteJSONError(w http.ResponseWriter, r *http.Request, msg string, code string, status int) {
	traceID := ""
	if r != nil {
		traceparent := r.Header.Get("traceparent")
		if traceparent != "" {
			parts := strings.Split(traceparent, "-")
			if len(parts) >= 2 {
				traceID = parts[1]
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{
		Error:   msg,
		Code:    code,
		TraceID: traceID,
	})
}

func CheckDeprecationsAndAddHeader(w http.ResponseWriter, ctx context.Context, name, version string) {
	metadataKey := fmt.Sprintf("%s/metadata.json", name)
	resp, err := S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(BucketName),
		Key:    aws.String(metadataKey),
	})
	if err == nil {
		defer resp.Body.Close()
		var metadata PackageMetadata
		metaData, merr := io.ReadAll(resp.Body)
		if merr == nil && json.Unmarshal(metaData, &metadata) == nil {
			if vd, ok := metadata.Versions[version]; ok && vd.Deprecated {
				w.Header().Set("X-Deprecation-Warning", vd.DeprecationMsg)
			}
		}
	}
}
