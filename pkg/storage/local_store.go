package storage

import (
	"bytes"
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
	"servstore/pkg/wasm"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/zeebo/blake3"
)

type LocalStore struct {
	rootDir       string
	mu            sync.RWMutex
	pebbleDB      *pebble.DB
	encryptionKey []byte // nil means encryption disabled
	coldTier      *ColdTierManager // nil means cold-tiering disabled
	coldCancel    context.CancelFunc // cancel the background sweep goroutine
}

func NewLocalStore(rootDir string) (*LocalStore, error) {
	absPath, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absPath, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(absPath, ".metadata_db")
	db, err := pebble.Open(dbPath, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to open pebble db: %w", err)
	}

	return &LocalStore{
		rootDir:  absPath,
		pebbleDB: db,
	}, nil
}

func (s *LocalStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.coldCancel != nil {
		s.coldCancel()
	}
	if s.pebbleDB != nil {
		err := s.pebbleDB.Close()
		s.pebbleDB = nil
		return err
	}
	return nil
}

// WithEncryptionKey configures AES-256 encryption at rest using the given passphrase.
// The passphrase is hashed with SHA-256 to produce a 32-byte key.
func (s *LocalStore) WithEncryptionKey(passphrase string) {
	s.encryptionKey = deriveKey(passphrase)
}

func (s *LocalStore) getBucketDir(bucket string) string {
	return filepath.Join(s.rootDir, bucket)
}

func (s *LocalStore) getObjectDataPath(bucket, key, versionID string) string {
	if strings.HasPrefix(versionID, "cas-") {
		return filepath.Join(s.getBucketDir(bucket), ".data", "cas."+strings.TrimPrefix(versionID, "cas-"))
	}
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
	if _, err := s.readBucketMeta(bucket); err == nil {
		return ErrBucketExists
	}

	if err := os.MkdirAll(filepath.Join(bucketDir, ".data"), 0755); err != nil {
		return err
	}

	meta := Bucket{
		Name:        bucket,
		CreatedTime: time.Now(),
		Versioning:  "Disabled",
	}

	return s.writeBucketMeta(bucket, &meta)
}

func (s *LocalStore) DeleteBucket(ctx context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.readBucketMeta(bucket); err != nil {
		return err
	}

	// Verify the bucket is empty by scanning Pebble for any objects in this bucket
	prefix := []byte("o:" + bucket + ":")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	hasObjects := false
	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); {
		var om ObjectMeta
		if json.Unmarshal(iter.Value(), &om) == nil && len(om.Versions) > 0 {
			hasObjects = true
			break
		}
		break
	}
	if hasObjects {
		return fmt.Errorf("bucket is not empty")
	}

	// Delete bucket metadata
	if err := s.pebbleDB.Delete([]byte("b:"+bucket), pebble.Sync); err != nil {
		return err
	}

	// Clean up data directories on disk
	bucketDir := s.getBucketDir(bucket)
	return os.RemoveAll(bucketDir)
}

func (s *LocalStore) ListBuckets(ctx context.Context) ([]Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := []byte("b:")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var buckets []Bucket
	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
		var b Bucket
		if err := json.Unmarshal(iter.Value(), &b); err == nil {
			buckets = append(buckets, b)
		}
	}
	return buckets, nil
}

func (s *LocalStore) readBucketMeta(bucket string) (*Bucket, error) {
	key := []byte("b:" + bucket)
	val, closer, err := s.pebbleDB.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrBucketNotFound
		}
		return nil, err
	}
	defer closer.Close()
	var b Bucket
	if err := json.Unmarshal(val, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *LocalStore) writeBucketMeta(bucket string, b *Bucket) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	key := []byte("b:" + bucket)
	return s.pebbleDB.Set(key, data, pebble.Sync)
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
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) SetBucketContentAddressable(ctx context.Context, bucket string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.ContentAddressable = enabled
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) readObjectMeta(bucket, key string) (*ObjectMeta, error) {
	dbKey := []byte("o:" + bucket + ":" + key)
	data, closer, err := s.pebbleDB.Get(dbKey)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	defer closer.Close()
	var om ObjectMeta
	if err := json.Unmarshal(data, &om); err != nil {
		return nil, err
	}
	return &om, nil
}

func (s *LocalStore) writeObjectMeta(bucket, key string, om *ObjectMeta) error {
	dbKey := []byte("o:" + bucket + ":" + key)
	data, err := json.Marshal(om)
	if err != nil {
		return err
	}
	return s.pebbleDB.Set(dbKey, data, pebble.Sync)
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
	if val := ctx.Value(VersionIDContextKey); val != nil {
		versionID = val.(string)
	} else if b.Versioning == "Enabled" {
		versionID = generateUUID()
	} else {
		versionID = "null"
	}

	// WORM check: if versioning is disabled/suspended and the existing "null" version
	// is locked, reject the overwrite.
	if b.Versioning != "Enabled" {
		if existing, err2 := s.readObjectMeta(bucket, key); err2 == nil {
			for _, ver := range existing.Versions {
				if ver.VersionID == "null" && ver.isLocked() {
					return nil, ErrObjectLocked
				}
			}
		}
	}

	hasher := md5.New()
	// Read all data first so we can encrypt before writing
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	written := int64(len(plaintext))
	if size > 0 && written != size {
		return nil, fmt.Errorf("size mismatch: expected %d, got %d", size, written)
	}
	hasher.Write(plaintext)
	etag := hex.EncodeToString(hasher.Sum(nil))

	b3Checksum := ParallelBlake3Hash(plaintext)

	// If bucket is ContentAddressable, use cas-<checksum> as the version ID
	if b.ContentAddressable {
		versionID = "cas-" + b3Checksum
	}

	dataPath := s.getObjectDataPath(bucket, key, versionID)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return nil, err
	}

	// For CAS, check if target file already exists (deduplication)
	alreadyExists := false
	if b.ContentAddressable {
		if _, err := os.Stat(dataPath); err == nil {
			alreadyExists = true
		}
	}

	if !alreadyExists {
		tmpFile, err := os.CreateTemp(filepath.Dir(dataPath), "put-object-*")
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
		}()

		// Encrypt if key is configured
		var payload []byte
		if s.encryptionKey != nil {
			payload, err = encryptPayload(s.encryptionKey, plaintext)
			if err != nil {
				return nil, err
			}
		} else {
			payload = plaintext
		}

		// DirectIO logic: Bypass OS page cache for large writes (>16MB)
		const directIOThreshold = 16 * 1024 * 1024
		if len(payload) >= directIOThreshold {
			_ = tmpFile.Close() // Close placeholder temp file
			if err := WriteFileDirectIO(dataPath, payload); err != nil {
				return nil, err
			}
		} else {
			if _, err := tmpFile.Write(payload); err != nil {
				return nil, err
			}
			// Close temp file before renaming
			_ = tmpFile.Close()
			if err := os.Rename(tmpFile.Name(), dataPath); err != nil {
				return nil, err
			}
		}
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
		Checksum:       b3Checksum,
	}

	// Adjust existing versions: IsLatest must be set to false for other versions
	for i := range om.Versions {
		if b.Versioning == "Enabled" || b.ContentAddressable {
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
	if timeVal := ctx.Value(TimeTravelContextKey); timeVal != nil {
		if t, ok := timeVal.(time.Time); ok {
			// Find the latest version active at or before time t
			// Note: om.Versions is sorted newest first. So we scan from oldest (end of slice) to newest (start of slice)
			// to find the last one created before/at t.
			for i := len(om.Versions) - 1; i >= 0; i-- {
				if om.Versions[i].LastModified.Before(t) || om.Versions[i].LastModified.Equal(t) {
					targetVer = &om.Versions[i]
				}
			}
		}
	}

	if targetVer == nil {
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
	}

	if targetVer == nil {
		return nil, nil, ErrObjectNotFound
	}

	if targetVer.IsDeleteMarker {
		// In S3, getting a delete marker returns 404 (or specific header if versionId is requested)
		return nil, targetVer, ErrObjectNotFound
	}

	dataPath := s.getObjectDataPath(bucket, key, targetVer.VersionID)

	// Cold-tier re-hydration: if the data file is absent but a .cold stub exists,
	// transparently fetch the block back from the remote before opening it.
	if _, statErr := os.Stat(dataPath); os.IsNotExist(statErr) {
		sp := stubPath(dataPath)
		if _, stubErr := os.Stat(sp); stubErr == nil && s.coldTier != nil {
			if hydrateErr := s.coldTier.FetchBack(context.Background(), dataPath); hydrateErr != nil {
				return nil, nil, fmt.Errorf("cold tier re-hydration failed: %w", hydrateErr)
			}
		}
	}

	if s.encryptionKey != nil {
		// Read, decrypt, and return an in-memory reader
		ciphertext, err := os.ReadFile(dataPath)
		if err != nil {
			return nil, nil, err
		}
		plaintext, err := decryptPayload(s.encryptionKey, ciphertext)
		if err != nil {
			return nil, nil, err
		}
		if targetVer.Checksum != "" {
			b3Hasher := blake3.New()
			b3Hasher.Write(plaintext)
			sum := hex.EncodeToString(b3Hasher.Sum(nil))
			if sum != targetVer.Checksum {
				return nil, nil, fmt.Errorf("data integrity corruption detected (BLAKE3 mismatch): expected %s, got %s", targetVer.Checksum, sum)
			}
		}
		return io.NopCloser(bytes.NewReader(plaintext)), targetVer, nil
	}

	file, err := os.Open(dataPath)
	if err != nil {
		return nil, nil, err
	}

	if targetVer.Checksum != "" {
		return &integrityCheckingReader{
			rc:       file,
			hasher:   blake3.New(),
			expected: targetVer.Checksum,
		}, targetVer, nil
	}

	return file, targetVer, nil
}

type integrityCheckingReader struct {
	rc       io.ReadCloser
	hasher   *blake3.Hasher
	expected string
}

func (r *integrityCheckingReader) Read(p []byte) (n int, err error) {
	n, err = r.rc.Read(p)
	if n > 0 {
		r.hasher.Write(p[:n])
	}
	if err == io.EOF {
		sum := hex.EncodeToString(r.hasher.Sum(nil))
		if sum != r.expected {
			return n, fmt.Errorf("data integrity corruption detected (BLAKE3 mismatch): expected %s, got %s", r.expected, sum)
		}
	}
	return n, err
}

func (r *integrityCheckingReader) Close() error {
	return r.rc.Close()
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
	if timeVal := ctx.Value(TimeTravelContextKey); timeVal != nil {
		if t, ok := timeVal.(time.Time); ok {
			for i := len(om.Versions) - 1; i >= 0; i-- {
				if om.Versions[i].LastModified.Before(t) || om.Versions[i].LastModified.Equal(t) {
					targetVer = &om.Versions[i]
				}
			}
		}
	}

	if targetVer == nil {
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

		// WORM check: reject deletion of a locked version
		if om.Versions[foundIndex].isLocked() {
			return nil, ErrObjectLocked
		}

		deletedVer := om.Versions[foundIndex]
		if !deletedVer.IsDeleteMarker {
			dataPath := s.getObjectDataPath(bucket, key, versionID)
			
			// If CAS, do reference counting before removing the data file
			if strings.HasPrefix(versionID, "cas-") {
				refCount := 0
				// Scan all metadata keys in this bucket
				metaKeys, scanErr := s.scanMetadataKeys(bucket)
				if scanErr == nil {
					for _, k := range metaKeys {
						// Don't recount ourselves since we're about to be deleted
						if k == key {
							// For our own object, count only other versions
							for _, otherVer := range om.Versions {
								if otherVer.VersionID == versionID && !otherVer.IsDeleteMarker {
									// wait, we are removing foundIndex from om.Versions below,
									// so let's only count if it's NOT the version we're deleting.
								}
							}
							continue
						}
						if otherMeta, err := s.readObjectMeta(bucket, k); err == nil {
							for _, otherVer := range otherMeta.Versions {
								if otherVer.VersionID == versionID && !otherVer.IsDeleteMarker {
									refCount++
								}
							}
						}
					}
				}
				// Also count other versions of the same object we are currently editing
				for i, otherVer := range om.Versions {
					if i != foundIndex && otherVer.VersionID == versionID && !otherVer.IsDeleteMarker {
						refCount++
					}
				}

				if refCount == 0 {
					_ = os.Remove(dataPath)
				}
			} else {
				_ = os.Remove(dataPath)
			}
		}

		// Remove from slice
		om.Versions = append(om.Versions[:foundIndex], om.Versions[foundIndex+1:]...)

		// If we deleted the latest, make the next one latest
		if deletedVer.IsLatest && len(om.Versions) > 0 {
			om.Versions[0].IsLatest = true
		}

		// If no versions left, delete the metadata file entirely
		if len(om.Versions) == 0 {
			_ = s.pebbleDB.Delete([]byte("o:"+bucket+":"+key), pebble.Sync)
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
		// WORM check: if the latest version is locked, reject placing a delete marker
		for _, ver := range om.Versions {
			if ver.IsLatest && ver.isLocked() {
				return nil, ErrObjectLocked
			}
		}
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
			if !ver.IsDeleteMarker {
				dataPath := s.getObjectDataPath(bucket, key, ver.VersionID)
				if strings.HasPrefix(ver.VersionID, "cas-") {
					refCount := 0
					metaKeys, scanErr := s.scanMetadataKeys(bucket)
					if scanErr == nil {
						for _, k := range metaKeys {
							if k == key {
								continue
							}
							if otherMeta, err := s.readObjectMeta(bucket, k); err == nil {
								for _, otherVer := range otherMeta.Versions {
									if otherVer.VersionID == ver.VersionID && !otherVer.IsDeleteMarker {
										refCount++
									}
								}
							}
						}
					}
					if refCount == 0 {
						_ = os.Remove(dataPath)
					}
				} else {
					_ = os.Remove(dataPath)
				}
			}
		}
		_ = s.pebbleDB.Delete([]byte("o:"+bucket+":"+key), pebble.Sync)
		return &ObjectVersion{Key: key, IsDeleteMarker: true}, nil
	}
}

// Helper to list keys in .metadata recursively
func (s *LocalStore) scanMetadataKeys(bucket string) ([]string, error) {
	prefix := []byte("o:" + bucket + ":")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var keys []string
	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
		k := string(iter.Key())
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 3 {
			keys = append(keys, parts[2])
		}
	}
	return keys, nil
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

	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	written := int64(len(plaintext))
	if size > 0 && written != size {
		return "", fmt.Errorf("size mismatch for part: expected %d, got %d", size, written)
	}

	hasher := md5.New()
	hasher.Write(plaintext)
	etag := hex.EncodeToString(hasher.Sum(nil))

	// Encrypt each part individually if key is configured
	var payload []byte
	if s.encryptionKey != nil {
		payload, err = encryptPayload(s.encryptionKey, plaintext)
		if err != nil {
			return "", err
		}
	} else {
		payload = plaintext
	}

	if err := os.WriteFile(partPath, payload, 0644); err != nil {
		return "", err
	}

	return etag, nil
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

	var totalSize int64
	var assembledPlaintext []byte
	for _, part := range parts {
		partPath := s.getPartPath(bucket, uploadID, part.PartNumber)
		partData, err := os.ReadFile(partPath)
		if err != nil {
			return nil, fmt.Errorf("missing or invalid part %d: %w", part.PartNumber, err)
		}

		// Decrypt each part before assembling
		var partPlain []byte
		if s.encryptionKey != nil {
			partPlain, err = decryptPayload(s.encryptionKey, partData)
			if err != nil {
				return nil, fmt.Errorf("decrypt part %d: %w", part.PartNumber, err)
			}
		} else {
			partPlain = partData
		}
		assembledPlaintext = append(assembledPlaintext, partPlain...)
		totalSize += int64(len(partPlain))
	}

	hasher.Write(assembledPlaintext)

	// Encrypt the complete assembled object before writing
	var finalPayload []byte
	if s.encryptionKey != nil {
		finalPayload, err = encryptPayload(s.encryptionKey, assembledPlaintext)
		if err != nil {
			return nil, err
		}
	} else {
		finalPayload = assembledPlaintext
	}

	if _, err := outFile.Write(finalPayload); err != nil {
		return nil, err
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

// isLocked returns true if this version has an active WORM lock.
func (v *ObjectVersion) isLocked() bool {
	return v.Locked && v.RetainUntil != nil && time.Now().Before(*v.RetainUntil)
}

// ---------- Lifecycle ----------

func (s *LocalStore) SetBucketLifecycle(ctx context.Context, bucket string, rules []LifecycleRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}
	b.Lifecycle = rules
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) GetBucketLifecycle(ctx context.Context, bucket string) ([]LifecycleRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}
	return b.Lifecycle, nil
}

func (s *LocalStore) DeleteBucketLifecycle(ctx context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}
	b.Lifecycle = nil
	return s.writeBucketMeta(bucket, b)
}

// ApplyLifecycle scans all buckets and permanently deletes object versions
// that have exceeded their lifecycle expiry days. Locked (WORM) versions are skipped.
// This is intended to be called periodically from a background goroutine.
func (s *LocalStore) ApplyLifecycle(ctx context.Context) (expired int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		return 0, err
	}

	now := time.Now()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bucketName := entry.Name()
		b, err := s.readBucketMeta(bucketName)
		if err != nil || len(b.Lifecycle) == 0 {
			continue
		}

		keys, err := s.scanMetadataKeys(bucketName)
		if err != nil {
			continue
		}

		for _, key := range keys {
			om, err := s.readObjectMeta(bucketName, key)
			if err != nil {
				continue
			}

			var surviving []ObjectVersion
			changed := false

			for _, ver := range om.Versions {
				if ver.IsDeleteMarker || ver.isLocked() {
					surviving = append(surviving, ver)
					continue
				}

				// Find the first matching enabled rule for this key
				matched := false
				for _, rule := range b.Lifecycle {
					if !rule.Enabled || rule.ExpirationDays <= 0 {
						continue
					}
					if rule.Prefix != "" && !strings.HasPrefix(key, rule.Prefix) {
						continue
					}
					age := now.Sub(ver.LastModified)
					if age >= time.Duration(rule.ExpirationDays)*24*time.Hour {
						// Expire this version
						dataPath := s.getObjectDataPath(bucketName, key, ver.VersionID)
						_ = os.Remove(dataPath)
						expired++
						matched = true
						changed = true
						break
					}
				}
				if !matched {
					surviving = append(surviving, ver)
				}
			}

			if !changed {
				continue
			}

			if len(surviving) == 0 {
				_ = s.pebbleDB.Delete([]byte("o:"+bucketName+":"+key), pebble.Sync)
				continue
			}

			// Ensure at least one version is marked latest
			hasLatest := false
			for _, v := range surviving {
				if v.IsLatest {
					hasLatest = true
					break
				}
			}
			if !hasLatest {
				surviving[0].IsLatest = true
			}

			om.Versions = surviving
			_ = s.writeObjectMeta(bucketName, key, om)
		}
	}

	return expired, nil
}


// LockObject sets a WORM retain-until date on a specific object version.
// Once locked, the version cannot be deleted or overwritten until the date passes.
func (s *LocalStore) LockObject(ctx context.Context, bucket, key, versionID string, retainUntil time.Time) (*ObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	// Default to latest version if no versionID supplied
	if versionID == "" {
		for i := range om.Versions {
			if om.Versions[i].IsLatest {
				versionID = om.Versions[i].VersionID
				break
			}
		}
	}

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

	// Only allow extending a lock, not shortening it (WORM compliance)
	if om.Versions[foundIndex].RetainUntil != nil && retainUntil.Before(*om.Versions[foundIndex].RetainUntil) {
		return nil, fmt.Errorf("cannot shorten an existing WORM lock: current retain-until is %s", om.Versions[foundIndex].RetainUntil.Format(time.RFC3339))
	}

	om.Versions[foundIndex].Locked = true
	om.Versions[foundIndex].RetainUntil = &retainUntil

	if err := s.writeObjectMeta(bucket, key, om); err != nil {
		return nil, err
	}

	result := om.Versions[foundIndex]
	return &result, nil
}

func (s *LocalStore) PutUserPolicy(ctx context.Context, username string, policy []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte("p:" + username)
	return s.pebbleDB.Set(key, policy, pebble.Sync)
}

func (s *LocalStore) GetUserPolicy(ctx context.Context, username string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte("p:" + username)
	data, closer, err := s.pebbleDB.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer closer.Close()
	return data, nil
}

func (s *LocalStore) DeleteUserPolicy(ctx context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte("p:" + username)
	err := s.pebbleDB.Delete(key, pebble.Sync)
	if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return err
	}
	return nil
}

func (s *LocalStore) ListLocalKeys(ctx context.Context) ([]LocalKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []LocalKey
	prefix := []byte("o:")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
		k := string(iter.Key())
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 3 {
			keys = append(keys, LocalKey{
				Bucket: parts[1],
				Key:    parts[2],
			})
		}
	}
	return keys, nil
}

func (s *LocalStore) SemanticSearch(ctx context.Context, bucket, query string, limit int) ([]*ObjectVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	keys, err := s.scanMetadataKeys(bucket)
	if err != nil {
		return nil, err
	}

	queryTokens := Tokenize(query)
	queryTF := ComputeTF(queryTokens)

	type match struct {
		ver   *ObjectVersion
		score float64
	}
	var matches []match

	for _, key := range keys {
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

		// Read file content if it is a text file
		if strings.HasPrefix(latest.ContentType, "text/") || strings.Contains(key, ".txt") || strings.Contains(key, ".md") {
			dataPath := s.getObjectDataPath(bucket, key, latest.VersionID)
			fileData, err := os.ReadFile(dataPath)
			if err != nil {
				continue
			}

			// If encrypted, decrypt it first
			var plaintext []byte
			if s.encryptionKey != nil {
				plaintext, err = decryptPayload(s.encryptionKey, fileData)
				if err != nil {
					continue
				}
			} else {
				plaintext = fileData
			}

			docTokens := Tokenize(string(plaintext))
			docTF := ComputeTF(docTokens)

			score := CosineSimilarity(queryTF, docTF)
			if score > 0.05 { // threshold to filter out irrelevant docs
				matches = append(matches, match{ver: latest, score: score})
			}
		}
	}

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	var results []*ObjectVersion
	for i := 0; i < len(matches) && (limit <= 0 || i < limit); i++ {
		results = append(results, matches[i].ver)
	}

	return results, nil
}

// WASMTransform reads the WASM binary stored at wasmKey, reads the target
// object at targetKey (optional versionID), runs the binary in a sandboxed
// wazero runtime with the target data piped to stdin, and returns the bytes
// written to stdout together with the target object's original content-type.
func (s *LocalStore) WASMTransform(ctx context.Context, bucket, wasmKey, targetKey, versionID string, memLimitMB, timeoutSec int) ([]byte, string, error) {
	// 1. Read WASM binary (stored as a normal object)
	wasmRC, _, err := s.GetObject(ctx, bucket, wasmKey, "")
	if err != nil {
		return nil, "", fmt.Errorf("wasm transform: get wasm binary %q: %w", wasmKey, err)
	}
	defer wasmRC.Close()
	wasmBytes, err := io.ReadAll(wasmRC)
	if err != nil {
		return nil, "", fmt.Errorf("wasm transform: read wasm binary: %w", err)
	}

	// 2. Read target object
	targetRC, targetVer, err := s.GetObject(ctx, bucket, targetKey, versionID)
	if err != nil {
		return nil, "", fmt.Errorf("wasm transform: get target %q: %w", targetKey, err)
	}
	defer targetRC.Close()
	targetBytes, err := io.ReadAll(targetRC)
	if err != nil {
		return nil, "", fmt.Errorf("wasm transform: read target: %w", err)
	}

	// 3. Execute inside isolated sandbox
	output, err := wasm.Execute(ctx, wasmBytes, targetBytes, memLimitMB, timeoutSec)
	if err != nil {
		return nil, "", err
	}
	return output, targetVer.ContentType, nil
}

// GetObjectBytes reads the object at (bucket, key, versionID) fully into memory
// and returns the raw bytes. It is a convenience wrapper around GetObject for
// cases where the entire payload must be buffered (e.g. WASM binaries or
// pipeline stage inputs).
func (s *LocalStore) GetObjectBytes(ctx context.Context, bucket, key, versionID string) ([]byte, error) {
	rc, _, err := s.GetObject(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// SetColdTier configures and starts the cold-storage tiering background sweep.
// Calling SetColdTier again replaces the previous configuration and restarts
// the sweep goroutine.
func (s *LocalStore) SetColdTier(cfg ColdTierConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Cancel any existing sweep goroutine
	if s.coldCancel != nil {
		s.coldCancel()
	}

	mgr := newColdTierManager(s, cfg)
	s.coldTier = mgr

	sweepCtx, cancel := context.WithCancel(context.Background())
	s.coldCancel = cancel
	mgr.Start(sweepCtx)
	return nil
}

// GetColdTierConfig returns the active cold-tier config and whether one is set.
func (s *LocalStore) GetColdTierConfig() (ColdTierConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.coldTier == nil {
		return ColdTierConfig{}, false
	}
	return s.coldTier.cfg, true
}

// RunColdSweep triggers an immediate cold-tier archival sweep.
// Satisfies the optional sweeper interface used by the S3 API handler.
// Returns the number of blocks archived and any accumulated errors.
func (s *LocalStore) RunColdSweep(ctx context.Context) (int, []error) {
	s.mu.RLock()
	mgr := s.coldTier
	s.mu.RUnlock()
	if mgr == nil {
		return 0, nil
	}
	return mgr.RunSweep(ctx)
}
