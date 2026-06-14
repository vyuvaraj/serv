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
)

type ObjectVersion struct {
	VersionID      string    `json:"version_id"`
	Key            string    `json:"key"`
	Size           int64     `json:"size"`
	LastModified   time.Time `json:"last_modified"`
	ETag           string    `json:"etag"`
	ContentType    string    `json:"content_type"`
	IsLatest       bool      `json:"is_latest"`
	IsDeleteMarker bool      `json:"is_delete_marker"`
}

type ObjectMeta struct {
	Key      string          `json:"key"`
	Versions []ObjectVersion `json:"versions"`
}

type Bucket struct {
	Name        string    `json:"name"`
	CreatedTime time.Time `json:"created_time"`
	Versioning  string    `json:"versioning"` // "Enabled", "Suspended", "Disabled"
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
}
