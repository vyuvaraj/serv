package storage

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"servstore/pkg/otel"
	"sort"
	"strings"
	"sync"
	"time"
)

type LocalStore struct {
	rootDir string
	mu      sync.RWMutex
}

func NewLocalStore(rootDir string) (*LocalStore, error) {
	absPath, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absPath, 0755); err != nil {
		return nil, err
	}
	return &LocalStore{rootDir: absPath}, nil
}

func (s *LocalStore) getBucketDir(bucket string) string {
	return filepath.Join(s.rootDir, bucket)
}

func (s *LocalStore) getBucketMetaPath(bucket string) string {
	return filepath.Join(s.getBucketDir(bucket), "bucket.json")
}

func (s *LocalStore) getObjectMetaPath(bucket, key string) string {
	return filepath.Join(s.getBucketDir(bucket), ".metadata", key+".json")
}

func (s *LocalStore) getObjectDataPath(bucket, key, versionID string) string {
	return filepath.Join(s.getBucketDir(bucket), ".data", key+"."+versionID)
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *LocalStore) CreateBucket(ctx context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bucketDir := s.getBucketDir(bucket)
	if _, err := os.Stat(bucketDir); err == nil {
		return ErrBucketExists
	}

	if err := os.MkdirAll(filepath.Join(bucketDir, ".metadata"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(bucketDir, ".data"), 0755); err != nil {
		return err
	}

	meta := Bucket{
		Name:        bucket,
		CreatedTime: time.Now(),
		Versioning:  "Disabled",
	}

	metaData, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	return os.WriteFile(s.getBucketMetaPath(bucket), metaData, 0644)
}

func (s *LocalStore) DeleteBucket(ctx context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bucketDir := s.getBucketDir(bucket)
	if _, err := os.Stat(bucketDir); os.IsNotExist(err) {
		return ErrBucketNotFound
	}

	// Verify the bucket is empty (except for metadata and bucket.json)
	// In S3, bucket must be empty to be deleted.
	// We can check if any user objects exist.
	metaDir := filepath.Join(bucketDir, ".metadata")
	_, err := os.ReadDir(metaDir)
	if err == nil {
		// Filter out directory entries that might not be objects or check if empty
		hasObjects := false
		var checkEmpty func(string) bool
		checkEmpty = func(path string) bool {
			entries, err := os.ReadDir(path)
			if err != nil {
				return false
			}
			for _, entry := range entries {
				if entry.IsDir() {
					if checkEmpty(filepath.Join(path, entry.Name())) {
						return true
					}
				} else {
					// Check if there are active versions
					data, err := os.ReadFile(filepath.Join(path, entry.Name()))
					if err == nil {
						var objMeta ObjectMeta
						if json.Unmarshal(data, &objMeta) == nil && len(objMeta.Versions) > 0 {
							return true
						}
					}
				}
			}
			return false
		}
		hasObjects = checkEmpty(metaDir)
		if hasObjects {
			return fmt.Errorf("bucket is not empty")
		}
	}

	return os.RemoveAll(bucketDir)
}

func (s *LocalStore) ListBuckets(ctx context.Context) ([]Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		return nil, err
	}

	var buckets []Bucket
	for _, entry := range entries {
		if entry.IsDir() {
			b, err := s.readBucketMeta(entry.Name())
			if err == nil {
				buckets = append(buckets, *b)
			}
		}
	}
	return buckets, nil
}

func (s *LocalStore) readBucketMeta(bucket string) (*Bucket, error) {
	metaPath := s.getBucketMetaPath(bucket)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrBucketNotFound
		}
		return nil, err
	}
	var b Bucket
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *LocalStore) GetBucket(ctx context.Context, bucket string) (*Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readBucketMeta(bucket)
}

func (s *LocalStore) SetBucketVersioning(ctx context.Context, bucket string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.Versioning = status
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}

	return os.WriteFile(s.getBucketMetaPath(bucket), data, 0644)
}

func (s *LocalStore) readObjectMeta(bucket, key string) (*ObjectMeta, error) {
	metaPath := s.getObjectMetaPath(bucket, key)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	var om ObjectMeta
	if err := json.Unmarshal(data, &om); err != nil {
		return nil, err
	}
	return &om, nil
}

func (s *LocalStore) writeObjectMeta(bucket, key string, om *ObjectMeta) error {
	metaPath := s.getObjectMetaPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(om)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}

func (s *LocalStore) PutObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) (ov *ObjectVersion, err error) {
	_, span := otel.StartSpan(ctx, "Storage PutObject", 1)
	span.SetAttribute("s3.bucket", bucket)
	span.SetAttribute("s3.key", key)
	defer func() {
		status := 1
		if err != nil {
			status = 2
		}
		span.End(status)
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	// Prepare version ID
	var versionID string
	if b.Versioning == "Enabled" {
		versionID = generateUUID()
	} else {
		// Disabled or Suspended stores with "null" version ID
		versionID = "null"
	}

	// Read content and calculate MD5
	dataPath := s.getObjectDataPath(bucket, key, versionID)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return nil, err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(dataPath), "put-object-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	hasher := md5.New()
	mw := io.MultiWriter(tmpFile, hasher)

	written, err := io.Copy(mw, reader)
	if err != nil {
		return nil, err
	}
	if size > 0 && written != size {
		return nil, fmt.Errorf("size mismatch: expected %d, got %d", size, written)
	}

	etag := hex.EncodeToString(hasher.Sum(nil))

	// Close temp file before renaming
	_ = tmpFile.Close()
	if err := os.Rename(tmpFile.Name(), dataPath); err != nil {
		return nil, err
	}

	// Read existing metadata or create new
	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			om = &ObjectMeta{Key: key, Versions: []ObjectVersion{}}
		} else {
			return nil, err
		}
	}

	now := time.Now()
	newVer := ObjectVersion{
		VersionID:      versionID,
		Key:            key,
		Size:           written,
		LastModified:   now,
		ETag:           etag,
		ContentType:    contentType,
		IsLatest:       true,
		IsDeleteMarker: false,
	}

	// Adjust existing versions: IsLatest must be set to false for other versions
	for i := range om.Versions {
		if b.Versioning == "Enabled" {
			om.Versions[i].IsLatest = false
		} else {
			// If versioning is suspended or disabled, we replace the version with ID "null"
			if om.Versions[i].VersionID == "null" {
				// Delete old "null" data file
				oldDataPath := s.getObjectDataPath(bucket, key, "null")
				if oldDataPath != dataPath {
					_ = os.Remove(oldDataPath)
				}
				// Remove this version from slice, we'll append the new one
				om.Versions = append(om.Versions[:i], om.Versions[i+1:]...)
				break
			}
		}
	}

	// If there were other versions, clear their IsLatest flag
	for i := range om.Versions {
		om.Versions[i].IsLatest = false
	}

	// Append the new version to the list (newest first or just append and we sort later)
	om.Versions = append([]ObjectVersion{newVer}, om.Versions...)

	if err := s.writeObjectMeta(bucket, key, om); err != nil {
		return nil, err
	}

	return &newVer, nil
}

func (s *LocalStore) GetObject(ctx context.Context, bucket, key, versionID string) (rc io.ReadCloser, ov *ObjectVersion, err error) {
	_, span := otel.StartSpan(ctx, "Storage GetObject", 1)
	span.SetAttribute("s3.bucket", bucket)
	span.SetAttribute("s3.key", key)
	if versionID != "" {
		span.SetAttribute("s3.version_id", versionID)
	}
	defer func() {
		status := 1
		if err != nil {
			status = 2
		}
		span.End(status)
	}()

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err = s.readBucketMeta(bucket)
	if err != nil {
		return nil, nil, err
	}

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, nil, err
	}

	var targetVer *ObjectVersion
	if versionID == "" {
		// Find latest version
		for i := range om.Versions {
			if om.Versions[i].IsLatest {
				targetVer = &om.Versions[i]
				break
			}
		}
		if targetVer == nil && len(om.Versions) > 0 {
			targetVer = &om.Versions[0]
		}
	} else {
		for i := range om.Versions {
			if om.Versions[i].VersionID == versionID {
				targetVer = &om.Versions[i]
				break
			}
		}
	}

	if targetVer == nil {
		return nil, nil, ErrObjectNotFound
	}

	if targetVer.IsDeleteMarker {
		// In S3, getting a delete marker returns 404 (or specific header if versionId is requested)
		return nil, targetVer, ErrObjectNotFound
	}

	dataPath := s.getObjectDataPath(bucket, key, targetVer.VersionID)
	file, err := os.Open(dataPath)
	if err != nil {
		return nil, nil, err
	}

	return file, targetVer, nil
}

func (s *LocalStore) HeadObject(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	var targetVer *ObjectVersion
	if versionID == "" {
		for i := range om.Versions {
			if om.Versions[i].IsLatest {
				targetVer = &om.Versions[i]
				break
			}
		}
		if targetVer == nil && len(om.Versions) > 0 {
			targetVer = &om.Versions[0]
		}
	} else {
		for i := range om.Versions {
			if om.Versions[i].VersionID == versionID {
				targetVer = &om.Versions[i]
				break
			}
		}
	}

	if targetVer == nil {
		return nil, ErrObjectNotFound
	}

	if targetVer.IsDeleteMarker {
		return targetVer, ErrObjectNotFound
	}

	return targetVer, nil
}

func (s *LocalStore) DeleteObject(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	if versionID != "" {
		// Delete specific version permanently
		foundIndex := -1
		for i, ver := range om.Versions {
			if ver.VersionID == versionID {
				foundIndex = i
				break
			}
		}

		if foundIndex == -1 {
			return nil, ErrInvalidVersion
		}

		deletedVer := om.Versions[foundIndex]
		if !deletedVer.IsDeleteMarker {
			dataPath := s.getObjectDataPath(bucket, key, versionID)
			_ = os.Remove(dataPath)
		}

		// Remove from slice
		om.Versions = append(om.Versions[:foundIndex], om.Versions[foundIndex+1:]...)

		// If we deleted the latest, make the next one latest
		if deletedVer.IsLatest && len(om.Versions) > 0 {
			om.Versions[0].IsLatest = true
		}

		// If no versions left, delete the metadata file entirely
		if len(om.Versions) == 0 {
			_ = os.Remove(s.getObjectMetaPath(bucket, key))
			return &deletedVer, nil
		}

		if err := s.writeObjectMeta(bucket, key, om); err != nil {
			return nil, err
		}

		return &deletedVer, nil
	}

	// Delete without version ID:
	// - If versioning is Enabled/Suspended: Create a Delete Marker.
	// - If versioning is Disabled: Delete permanently.
	if b.Versioning == "Enabled" {
		// Clear IsLatest from existing
		for i := range om.Versions {
			om.Versions[i].IsLatest = false
		}

		delMarker := ObjectVersion{
			VersionID:      generateUUID(),
			Key:            key,
			Size:           0,
			LastModified:   time.Now(),
			ETag:           "",
			ContentType:    "",
			IsLatest:       true,
			IsDeleteMarker: true,
		}
		om.Versions = append([]ObjectVersion{delMarker}, om.Versions...)
		if err := s.writeObjectMeta(bucket, key, om); err != nil {
			return nil, err
		}
		return &delMarker, nil
	} else if b.Versioning == "Suspended" {
		// Suspend: overwrite "null" version with a delete marker (or append delete marker "null")
		for i := range om.Versions {
			om.Versions[i].IsLatest = false
			if om.Versions[i].VersionID == "null" {
				// Delete data file for old null version if it wasn't a delete marker
				if !om.Versions[i].IsDeleteMarker {
					_ = os.Remove(s.getObjectDataPath(bucket, key, "null"))
				}
				om.Versions = append(om.Versions[:i], om.Versions[i+1:]...)
				break
			}
		}

		delMarker := ObjectVersion{
			VersionID:      "null",
			Key:            key,
			Size:           0,
			LastModified:   time.Now(),
			ETag:           "",
			ContentType:    "",
			IsLatest:       true,
			IsDeleteMarker: true,
		}
		om.Versions = append([]ObjectVersion{delMarker}, om.Versions...)
		if err := s.writeObjectMeta(bucket, key, om); err != nil {
			return nil, err
		}
		return &delMarker, nil
	} else {
		// Versioning Disabled: Permanent deletion of all versions
		for _, ver := range om.Versions {
			_ = os.Remove(s.getObjectDataPath(bucket, key, ver.VersionID))
		}
		_ = os.Remove(s.getObjectMetaPath(bucket, key))
		return &ObjectVersion{Key: key, IsDeleteMarker: true}, nil
	}
}

// Helper to list keys in .metadata recursively
func (s *LocalStore) scanMetadataKeys(bucket string) ([]string, error) {
	metaDir := filepath.Join(s.getBucketDir(bucket), ".metadata")
	var keys []string
	err := filepath.Walk(metaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			rel, err := filepath.Rel(metaDir, path)
			if err == nil {
				key := strings.TrimSuffix(rel, ".json")
				// Convert windows slash to standard URL/S3 slash
				key = filepath.ToSlash(key)
				keys = append(keys, key)
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return keys, err
}

func (s *LocalStore) ListObjects(ctx context.Context, bucket, prefix, delimiter, marker string, maxKeys int) ([]*ObjectVersion, []string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, nil, err
	}

	keys, err := s.scanMetadataKeys(bucket)
	if err != nil {
		return nil, nil, err
	}

	sort.Strings(keys)

	var objects []*ObjectVersion
	var commonPrefixes []string
	seenPrefixes := make(map[string]bool)

	count := 0
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if marker != "" && key <= marker {
			continue
		}

		// Read the latest version
		om, err := s.readObjectMeta(bucket, key)
		if err != nil {
			continue
		}

		var latest *ObjectVersion
		for i := range om.Versions {
			if om.Versions[i].IsLatest {
				latest = &om.Versions[i]
				break
			}
		}
		if latest == nil && len(om.Versions) > 0 {
			latest = &om.Versions[0]
		}

		if latest == nil || latest.IsDeleteMarker {
			continue
		}

		// Delimiter logic
		if delimiter != "" {
			subKey := key[len(prefix):]
			idx := strings.Index(subKey, delimiter)
			if idx != -1 {
				prefixDir := prefix + subKey[:idx+1]
				if !seenPrefixes[prefixDir] {
					seenPrefixes[prefixDir] = true
					commonPrefixes = append(commonPrefixes, prefixDir)
				}
				continue
			}
		}

		objects = append(objects, latest)
		count++
		if maxKeys > 0 && count >= maxKeys {
			break
		}
	}

	sort.Strings(commonPrefixes)
	return objects, commonPrefixes, nil
}

func (s *LocalStore) ListObjectVersions(ctx context.Context, bucket, prefix, delimiter, keyMarker, versionIDMarker string, maxKeys int) ([]*ObjectVersion, []string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, nil, err
	}

	keys, err := s.scanMetadataKeys(bucket)
	if err != nil {
		return nil, nil, err
	}

	sort.Strings(keys)

	var versions []*ObjectVersion
	var commonPrefixes []string
	seenPrefixes := make(map[string]bool)

	count := 0
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if keyMarker != "" && key < keyMarker {
			continue
		}

		om, err := s.readObjectMeta(bucket, key)
		if err != nil {
			continue
		}

		// S3 lists versions in descending order of modification time (newest first)
		for _, ver := range om.Versions {
			if key == keyMarker && versionIDMarker != "" && ver.VersionID <= versionIDMarker {
				continue
			}

			// Delimiter logic
			if delimiter != "" {
				subKey := key[len(prefix):]
				idx := strings.Index(subKey, delimiter)
				if idx != -1 {
					prefixDir := prefix + subKey[:idx+1]
					if !seenPrefixes[prefixDir] {
						seenPrefixes[prefixDir] = true
						commonPrefixes = append(commonPrefixes, prefixDir)
					}
					continue
				}
			}

			vCopy := ver // Local copy
			versions = append(versions, &vCopy)
			count++
			if maxKeys > 0 && count >= maxKeys {
				break
			}
		}

		if maxKeys > 0 && count >= maxKeys {
			break
		}
	}

	sort.Strings(commonPrefixes)
	return versions, commonPrefixes, nil
}

func (s *LocalStore) getMultipartUploadDir(bucket, uploadID string) string {
	return filepath.Join(s.getBucketDir(bucket), ".uploads", uploadID)
}

func (s *LocalStore) getPartPath(bucket, uploadID string, partNumber int) string {
	return filepath.Join(s.getMultipartUploadDir(bucket, uploadID), fmt.Sprintf("%d", partNumber))
}

func (s *LocalStore) InitiateMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return "", err
	}

	uploadID := generateUUID()
	uploadDir := s.getMultipartUploadDir(bucket, uploadID)
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return "", err
	}

	return uploadID, nil
}

func (s *LocalStore) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int, reader io.Reader, size int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return "", err
	}

	uploadDir := s.getMultipartUploadDir(bucket, uploadID)
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		return "", fmt.Errorf("upload id not found")
	}

	partPath := s.getPartPath(bucket, uploadID, partNumber)
	file, err := os.Create(partPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := md5.New()
	mw := io.MultiWriter(file, hasher)

	written, err := io.Copy(mw, reader)
	if err != nil {
		return "", err
	}
	if size > 0 && written != size {
		return "", fmt.Errorf("size mismatch for part: expected %d, got %d", size, written)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *LocalStore) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []PartInfo, contentType string) (*ObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	uploadDir := s.getMultipartUploadDir(bucket, uploadID)
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("upload id not found")
	}

	// Prepare version ID
	var versionID string
	if b.Versioning == "Enabled" {
		versionID = generateUUID()
	} else {
		versionID = "null"
	}

	// Create output data file
	dataPath := s.getObjectDataPath(bucket, key, versionID)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return nil, err
	}

	outFile, err := os.Create(dataPath)
	if err != nil {
		return nil, err
	}
	defer outFile.Close()

	hasher := md5.New()
	mw := io.MultiWriter(outFile, hasher)

	var totalSize int64
	for _, part := range parts {
		partPath := s.getPartPath(bucket, uploadID, part.PartNumber)
		partFile, err := os.Open(partPath)
		if err != nil {
			return nil, fmt.Errorf("missing or invalid part %d: %w", part.PartNumber, err)
		}
		
		written, err := io.Copy(mw, partFile)
		partFile.Close()
		if err != nil {
			return nil, err
		}
		totalSize += written
	}

	etag := hex.EncodeToString(hasher.Sum(nil))

	// Cleanup upload parts folder
	_ = os.RemoveAll(uploadDir)

	// Read existing metadata or create new
	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			om = &ObjectMeta{Key: key, Versions: []ObjectVersion{}}
		} else {
			return nil, err
		}
	}

	now := time.Now()
	newVer := ObjectVersion{
		VersionID:      versionID,
		Key:            key,
		Size:           totalSize,
		LastModified:   now,
		ETag:           etag,
		ContentType:    contentType,
		IsLatest:       true,
		IsDeleteMarker: false,
	}

	// Adjust existing versions: IsLatest must be set to false for other versions
	for i := range om.Versions {
		if b.Versioning == "Enabled" {
			om.Versions[i].IsLatest = false
		} else {
			if om.Versions[i].VersionID == "null" {
				oldDataPath := s.getObjectDataPath(bucket, key, "null")
				if oldDataPath != dataPath {
					_ = os.Remove(oldDataPath)
				}
				om.Versions = append(om.Versions[:i], om.Versions[i+1:]...)
				break
			}
		}
	}

	for i := range om.Versions {
		om.Versions[i].IsLatest = false
	}

	om.Versions = append([]ObjectVersion{newVer}, om.Versions...)

	if err := s.writeObjectMeta(bucket, key, om); err != nil {
		return nil, err
	}

	return &newVer, nil
}

func (s *LocalStore) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	uploadDir := s.getMultipartUploadDir(bucket, uploadID)
	return os.RemoveAll(uploadDir)
}
