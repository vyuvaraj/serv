package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrBucketNotFound = errors.New("bucket not found")
	ErrBucketExists   = errors.New("bucket already exists")
	ErrObjectNotFound = errors.New("object not found")
	ErrInvalidVersion = errors.New("invalid version id")
	ErrObjectLocked   = errors.New("object is locked (WORM) and cannot be modified or deleted until the retain-until date")
)

type ObjectVersion struct {
	VersionID      string            `json:"version_id"`
	Key            string            `json:"key"`
	Size           int64             `json:"size"`
	LastModified   time.Time         `json:"last_modified"`
	ETag           string            `json:"etag"`
	ContentType    string            `json:"content_type"`
	IsLatest       bool              `json:"is_latest"`
	IsDeleteMarker bool              `json:"is_delete_marker"`
	Locked         bool              `json:"locked,omitempty"`       // WORM lock active
	RetainUntil    *time.Time        `json:"retain_until,omitempty"` // lock expiry (nil = no lock)
	Checksum       string            `json:"checksum,omitempty"`     // BLAKE3 checksum
	Tags           map[string]string `json:"tags,omitempty"`
	Compressed     bool              `json:"compressed,omitempty"`
}

type ObjectMeta struct {
	Key      string          `json:"key"`
	Versions []ObjectVersion `json:"versions"`
}

// LifecycleRule defines automatic object expiration for a bucket.
type LifecycleRule struct {
	ID             string `json:"id"`              // Unique rule identifier
	Enabled        bool   `json:"enabled"`
	Prefix         string `json:"prefix"`          // Apply only to keys with this prefix ("" = all)
	ExpirationDays int    `json:"expiration_days"` // Delete objects older than this many days
}

type EventNotificationRule struct {
	ID         string   `json:"id"`
	Events     []string `json:"events"`
	FilterKey  string   `json:"filter_key,omitempty"`
	WebhookURL string   `json:"webhook_url"`
}

type WASMTrigger struct {
	Event       string `json:"event"`                  // "ObjectCreated:Put", "ObjectRemoved:Delete", or "*"
	WASMKey     string `json:"wasm_key"`               // Key of the compiled WASM binary inside the bucket
	Prefix      string `json:"prefix,omitempty"`       // Filter key prefix
	Suffix      string `json:"suffix,omitempty"`       // Filter key suffix
	MemoryLimit int    `json:"memory_limit,omitempty"` // Memory page limit in MB, default 64
	Timeout     int    `json:"timeout,omitempty"`      // Wall-clock execution timeout in seconds, default 30
}

type Bucket struct {
	Name               string                  `json:"name"`
	CreatedTime        time.Time               `json:"created_time"`
	Versioning         string                  `json:"versioning"` // "Enabled", "Suspended", "Disabled"
	Lifecycle          []LifecycleRule         `json:"lifecycle,omitempty"`
	ContentAddressable bool                    `json:"content_addressable,omitempty"`
	Quota              int64                   `json:"quota,omitempty"`
	Triggers           []WASMTrigger           `json:"triggers,omitempty"`
	NotificationConfig []EventNotificationRule `json:"notification_config,omitempty"`
}

type PartInfo struct {
	PartNumber int    `xml:"PartNumber" json:"part_number"`
	ETag       string `xml:"ETag" json:"etag"`
}

type StorageEngine interface {
	CreateBucket(ctx context.Context, bucket string) error
	DeleteBucket(ctx context.Context, bucket string) error
	ListBuckets(ctx context.Context) ([]Bucket, error)
	GetBucket(ctx context.Context, bucket string) (*Bucket, error)
	SetBucketVersioning(ctx context.Context, bucket string, status string) error
	SetBucketContentAddressable(ctx context.Context, bucket string, enabled bool) error

	PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) (*ObjectVersion, error)
	GetObject(ctx context.Context, bucket, key, versionID string) (io.ReadCloser, *ObjectVersion, error)
	DeleteObject(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error)
	HeadObject(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error)
	ListObjects(ctx context.Context, bucket, prefix, delimiter, marker string, maxKeys int) ([]*ObjectVersion, []string, error)
	ListObjectVersions(ctx context.Context, bucket, prefix, delimiter, keyMarker, versionIDMarker string, maxKeys int) ([]*ObjectVersion, []string, error)

	InitiateMultipartUpload(ctx context.Context, bucket, key string) (string, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int, reader io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []PartInfo, contentType string) (*ObjectVersion, error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error

	LockObject(ctx context.Context, bucket, key, versionID string, retainUntil time.Time) (*ObjectVersion, error)
	PutObjectTagging(ctx context.Context, bucket, key, versionID string, tags map[string]string) (*ObjectVersion, error)
	GetObjectTagging(ctx context.Context, bucket, key, versionID string) (map[string]string, error)
	DeleteObjectTagging(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error)
	SetBucketQuota(ctx context.Context, bucket string, quota int64) error
	GetBucketQuota(ctx context.Context, bucket string) (int64, error)
	SetBucketTriggers(ctx context.Context, bucket string, triggers []WASMTrigger) error
	GetBucketTriggers(ctx context.Context, bucket string) ([]WASMTrigger, error)
	SetBucketNotifications(ctx context.Context, bucket string, rules []EventNotificationRule) error
	GetBucketNotifications(ctx context.Context, bucket string) ([]EventNotificationRule, error)



	SetBucketLifecycle(ctx context.Context, bucket string, rules []LifecycleRule) error
	GetBucketLifecycle(ctx context.Context, bucket string) ([]LifecycleRule, error)
	DeleteBucketLifecycle(ctx context.Context, bucket string) error

	PutUserPolicy(ctx context.Context, username string, policy []byte) error
	GetUserPolicy(ctx context.Context, username string) ([]byte, error)
	DeleteUserPolicy(ctx context.Context, username string) error

	PutSchema(ctx context.Context, service string, schema []byte) error
	GetSchema(ctx context.Context, service string) ([]byte, error)
	ListSchemas(ctx context.Context) (map[string][]byte, error)

	ListLocalKeys(ctx context.Context) ([]LocalKey, error)
	SemanticSearch(ctx context.Context, bucket, query string, limit int) ([]*ObjectVersion, error)

	// WASMTransform runs the WASM binary stored at wasmKey against the object
	// stored at targetKey and returns the transformed output bytes together with
	// the target object's original content-type.
	WASMTransform(ctx context.Context, bucket, wasmKey, targetKey, versionID string, memLimitMB, timeoutSec int) ([]byte, string, error)

	// GetObjectBytes reads the object at (bucket, key, versionID) fully into
	// memory and returns the raw bytes. It is a convenience wrapper around
	// GetObject that is safe to use when the entire payload needs to be
	// buffered — e.g. loading WASM binaries or feeding pipeline stage inputs.
	GetObjectBytes(ctx context.Context, bucket, key, versionID string) ([]byte, error)

	// SetColdTier attaches a ColdTierConfig to the store and starts background archival.
	SetColdTier(cfg ColdTierConfig) error
	// GetColdTierConfig returns the currently active cold-tier configuration.
	GetColdTierConfig() (ColdTierConfig, bool)
}

type LocalKey struct {
	Bucket string
	Key    string
}

type ContextKey string
const VersionIDContextKey ContextKey = "versionID"
const TimeTravelContextKey ContextKey = "timeTravel"


