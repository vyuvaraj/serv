package registry

import (
	"bytes"
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

type PackageStore interface {
	GetObject(ctx context.Context, key string) ([]byte, error)
	PutObject(ctx context.Context, key string, data []byte) error
	ListObjects(ctx context.Context) ([]string, error)
}

var ActiveStore PackageStore

type S3Store struct {
	Client *s3.Client
}

func (s *S3Store) GetObject(ctx context.Context, key string) ([]byte, error) {
	resp, err := s.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(BucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *S3Store) PutObject(ctx context.Context, key string, data []byte) error {
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(BucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	return err
}

func (s *S3Store) ListObjects(ctx context.Context) ([]string, error) {
	resp, err := s.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(BucketName),
	})
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, obj := range resp.Contents {
		keys = append(keys, *obj.Key)
	}
	return keys, nil
}

var (
	S3Client   *s3.Client
	BucketName = "serv-packages"

	PackageIndexMu sync.RWMutex
	PackageIndex   = make(map[string]*PackageIndexItem)

	// publishLocksMu guards the per-version publish lock map.
	publishLocksMu sync.Mutex
	// publishLocks holds one mutex per "name@version" key to prevent concurrent publishes
	// of the same version. The first caller acquires the lock; subsequent callers for
	// the same key will block, then discover the version already exists and return 409.
	publishLocks = make(map[string]*sync.Mutex)
)

// AcquirePublishLock acquires an exclusive per-version publish lock for "name@version".
// The caller MUST call ReleasePublishLock when done.
func AcquirePublishLock(name, version string) {
	key := name + "@" + version
	publishLocksMu.Lock()
	mu, ok := publishLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		publishLocks[key] = mu
	}
	publishLocksMu.Unlock()
	mu.Lock()
}

// ReleasePublishLock releases the per-version publish lock for "name@version".
func ReleasePublishLock(name, version string) {
	key := name + "@" + version
	publishLocksMu.Lock()
	mu, ok := publishLocks[key]
	publishLocksMu.Unlock()
	if ok {
		mu.Unlock()
	}
}


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
	keys, err := ActiveStore.ListObjects(ctx)
	if err != nil {
		log.Printf("Failed to list objects for indexing: %v", err)
		return
	}

	PackageIndexMu.Lock()
	defer PackageIndexMu.Unlock()

	PackageIndex = make(map[string]*PackageIndexItem)

	for _, key := range keys {
		if strings.HasSuffix(key, "/metadata.json") {
			data, err := ActiveStore.GetObject(ctx, key)
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
	metaData, err := ActiveStore.GetObject(ctx, metadataKey)
	if err == nil {
		var metadata PackageMetadata
		if json.Unmarshal(metaData, &metadata) == nil {
			if vd, ok := metadata.Versions[version]; ok && vd.Deprecated {
				w.Header().Set("X-Deprecation-Warning", vd.DeprecationMsg)
			}
		}
	}
}

func GetObject(ctx context.Context, key string) ([]byte, error) {
	return ActiveStore.GetObject(ctx, key)
}

func PutObject(ctx context.Context, key string, data []byte) error {
	return ActiveStore.PutObject(ctx, key, data)
}
