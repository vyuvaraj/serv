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
	VersionID      string     `json:"version_id"`
	Key            string     `json:"key"`
	Size           int64      `json:"size"`
	LastModified   time.Time  `json:"last_modified"`
	ETag           string     `json:"etag"`
	ContentType    string     `json:"content_type"`
	IsLatest       bool       `json:"is_latest"`
	IsDeleteMarker bool       `json:"is_delete_marker"`
	Locked         bool       `json:"locked,omitempty"`          // WORM lock active
	RetainUntil    *time.Time `json:"retain_until,omitempty"`    // lock expiry (nil = no lock)
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

type Bucket struct {
	Name       string          `json:"name"`
	CreatedTime time.Time      `json:"created_time"`
	Versioning  string         `json:"versioning"`  // "Enabled", "Suspended", "Disabled"
	Lifecycle  []LifecycleRule `json:"lifecycle,omitempty"`
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

	SetBucketLifecycle(ctx context.Context, bucket string, rules []LifecycleRule) error
	GetBucketLifecycle(ctx context.Context, bucket string) ([]LifecycleRule, error)
	DeleteBucketLifecycle(ctx context.Context, bucket string) error

	PutUserPolicy(ctx context.Context, username string, policy []byte) error
	GetUserPolicy(ctx context.Context, username string) ([]byte, error)
	DeleteUserPolicy(ctx context.Context, username string) error
}
