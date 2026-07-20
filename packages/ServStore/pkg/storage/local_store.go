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
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/otel"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/wasm"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/blake3"
)

type LocalStore struct {
	rootDir       string
	mu            sync.RWMutex
	pebbleDB      *pebble.DB
	encryptionKey []byte // nil means encryption disabled
	coldTier      *ColdTierManager // nil means cold-tiering disabled
	coldCancel    context.CancelFunc // cancel the background sweep goroutine
	hnswIndices   map[string]*HNSWIndex
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

	// Try to initialize ONNX if environment variables are provided
	onnxLib := os.Getenv("SERVSTORE_ONNX_LIB")
	onnxModel := os.Getenv("SERVSTORE_ONNX_MODEL")
	onnxVocab := os.Getenv("SERVSTORE_ONNX_VOCAB")
	if onnxLib != "" && onnxModel != "" {
		_ = InitializeONNX(onnxLib, onnxModel, onnxVocab)
	}

	store := &LocalStore{
		rootDir:     absPath,
		pebbleDB:    db,
		hnswIndices: make(map[string]*HNSWIndex),
	}

	if err := store.RecoverFromWAL(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to recover from WAL: %w", err)
	}

	return store, nil
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

func (s *LocalStore) CreateBucket(ctx context.Context, bucket string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil {
			_ = s.writeWAL(WALEntry{
				Timestamp: time.Now().UnixNano(),
				Operation: "CREATE_BUCKET",
				Bucket:    bucket,
			})
		}
	}()

	bucketDir := s.getBucketDir(bucket)
	if _, err2 := s.readBucketMeta(bucket); err2 == nil {
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

func (s *LocalStore) DeleteBucket(ctx context.Context, bucket string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil {
			_ = s.writeWAL(WALEntry{
				Timestamp: time.Now().UnixNano(),
				Operation: "DELETE_BUCKET",
				Bucket:    bucket,
			})
		}
	}()

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
	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
		var om ObjectMeta
		if json.Unmarshal(iter.Value(), &om) == nil && len(om.Versions) > 0 {
			hasObjects = true
			break
		}
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
	if s.pebbleDB == nil {
		return nil, fmt.Errorf("store is closed")
	}
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

func isCompressible(key, contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.HasPrefix(ct, "text/") ||
		ct == "application/json" ||
		ct == "application/xml" ||
		ct == "application/javascript" ||
		ct == "application/x-javascript" ||
		strings.HasSuffix(ct, "+json") ||
		strings.HasSuffix(ct, "+xml") {
		return true
	}
	k := strings.ToLower(key)
	return strings.HasSuffix(k, ".txt") ||
		strings.HasSuffix(k, ".md") ||
		strings.HasSuffix(k, ".json") ||
		strings.HasSuffix(k, ".xml") ||
		strings.HasSuffix(k, ".log")
}

func (s *LocalStore) getBucketUsageNoLock(bucket string) (int64, error) {
	prefix := []byte("o:" + bucket + ":")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	var totalSize int64
	for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
		var om ObjectMeta
		if json.Unmarshal(iter.Value(), &om) == nil {
			for _, ver := range om.Versions {
				if ver.IsLatest && !ver.IsDeleteMarker {
					totalSize += ver.Size
				}
			}
		}
	}
	return totalSize, nil
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

	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil && ov != nil {
			_ = s.writeWAL(WALEntry{
				Timestamp:   time.Now().UnixNano(),
				Operation:   "PUT_OBJECT",
				Bucket:      bucket,
				Key:         key,
				VersionID:   ov.VersionID,
				ContentType: contentType,
				Size:        ov.Size,
				Data:        plaintext,
			})
		}
	}()

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
	written := int64(len(plaintext))
	if size > 0 && written != size {
		return nil, fmt.Errorf("size mismatch: expected %d, got %d", size, written)
	}
	hasher.Write(plaintext)
	etag := hex.EncodeToString(hasher.Sum(nil))

	b3Checksum := ParallelBlake3Hash(plaintext)

	// Quota Check
	if b.Quota > 0 {
		usage, err := s.getBucketUsageNoLock(bucket)
		if err != nil {
			return nil, err
		}
		var existingSize int64
		if b.Versioning != "Enabled" {
			if existing, err2 := s.readObjectMeta(bucket, key); err2 == nil {
				for _, ver := range existing.Versions {
					if ver.VersionID == "null" && ver.IsLatest && !ver.IsDeleteMarker {
						existingSize = ver.Size
					}
				}
			}
		}
		if usage - existingSize + written > b.Quota {
			return nil, fmt.Errorf("quota exceeded: bucket has quota of %d bytes, upload would exceed it", b.Quota)
		}
	}

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

	isCompressed := false
	if !alreadyExists {
		tmpFile, err := os.CreateTemp(filepath.Dir(dataPath), "put-object-*")
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
		}()

		var toWrite []byte
		if isCompressible(key, contentType) {
			enc, err := zstd.NewWriter(nil)
			if err != nil {
				return nil, err
			}
			toWrite = enc.EncodeAll(plaintext, nil)
			isCompressed = true
		} else {
			toWrite = plaintext
		}

		// Encrypt if key is configured
		var finalBytes []byte
		if s.encryptionKey != nil {
			finalBytes, err = encryptPayload(s.encryptionKey, toWrite)
			if err != nil {
				return nil, err
			}
		} else {
			finalBytes = toWrite
		}

		// DirectIO logic: Bypass OS page cache for large writes (>16MB)
		const directIOThreshold = 16 * 1024 * 1024
		if len(finalBytes) >= directIOThreshold {
			_ = tmpFile.Close() // Close placeholder temp file
			if err := WriteFileDirectIO(dataPath, finalBytes); err != nil {
				return nil, err
			}
		} else {
			if _, err := tmpFile.Write(finalBytes); err != nil {
				return nil, err
			}
			// Close temp file before renaming
			_ = tmpFile.Close()
			if err := os.Rename(tmpFile.Name(), dataPath); err != nil {
				return nil, err
			}
		}
	} else {
		if isCompressible(key, contentType) {
			isCompressed = true
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
		Tags:           AutoClassify(key, contentType, plaintext),
		Compressed:     isCompressed,
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

	isText := strings.HasPrefix(contentType, "text/") || strings.Contains(key, ".txt") || strings.Contains(key, ".md")
	isImage := strings.HasPrefix(contentType, "image/") || strings.Contains(key, ".png") || strings.Contains(key, ".jpg") || strings.Contains(key, ".jpeg")
	isDoc := contentType == "application/pdf" || strings.Contains(key, ".pdf")
	isAudio := strings.HasPrefix(contentType, "audio/") || strings.Contains(key, ".mp3") || strings.Contains(key, ".wav")

	if isText || isImage || isDoc || isAudio {
		idx, err := s.getOrBuildHNSWIndexNoLock(bucket)
		if err == nil {
			var embeddingSource string
			if isText {
				embeddingSource = string(plaintext)
			} else if isImage {
				embeddingSource = "MOCK_CLIP_IMAGE_EMBEDDING_SOURCE: " + key
			} else if isDoc {
				embeddingSource = "MOCK_PDF_DOCUMENT_EMBEDDING_SOURCE: " + key
			} else if isAudio {
				embeddingSource = "MOCK_AUDIO_SPEECH_EMBEDDING_SOURCE: " + key
			}
			vector := GenerateEmbedding(embeddingSource)
			idx.Insert(key, vector)
		}
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
		decrypted, err := decryptPayload(s.encryptionKey, ciphertext)
		if err != nil {
			return nil, nil, err
		}

		var plaintext []byte
		if targetVer.Compressed {
			dec, err := zstd.NewReader(nil)
			if err != nil {
				return nil, nil, err
			}
			plaintext, err = dec.DecodeAll(decrypted, nil)
			dec.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("data integrity corruption detected (zstd decompression failed): %w", err)
			}
		} else {
			plaintext = decrypted
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

	if targetVer.Compressed {
		defer file.Close()
		compressed, err := io.ReadAll(file)
		if err != nil {
			return nil, nil, err
		}
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return nil, nil, err
		}
		plaintext, err := dec.DecodeAll(compressed, nil)
		dec.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("data integrity corruption detected (zstd decompression failed): %w", err)
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

func (s *LocalStore) DeleteObject(ctx context.Context, bucket, key, versionID string) (ov *ObjectVersion, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil && ov != nil {
			_ = s.writeWAL(WALEntry{
				Timestamp: time.Now().UnixNano(),
				Operation: "DELETE_OBJECT",
				Bucket:    bucket,
				Key:       key,
				VersionID: versionID,
			})
		}
	}()

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
			s.updateHNSWIndexOnDeleteNoLock(bucket, key, nil)
			return &deletedVer, nil
		}

		if err := s.writeObjectMeta(bucket, key, om); err != nil {
			return nil, err
		}

		s.updateHNSWIndexOnDeleteNoLock(bucket, key, om)
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
		s.updateHNSWIndexOnDeleteNoLock(bucket, key, om)
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
		s.updateHNSWIndexOnDeleteNoLock(bucket, key, om)
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
		s.updateHNSWIndexOnDeleteNoLock(bucket, key, nil)
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

	// Quota Check
	if b.Quota > 0 {
		usage, err := s.getBucketUsageNoLock(bucket)
		if err != nil {
			return nil, err
		}
		var existingSize int64
		if b.Versioning != "Enabled" {
			if existing, err2 := s.readObjectMeta(bucket, key); err2 == nil {
				for _, ver := range existing.Versions {
					if ver.VersionID == "null" && ver.IsLatest && !ver.IsDeleteMarker {
						existingSize = ver.Size
					}
				}
			}
		}
		if usage - existingSize + totalSize > b.Quota {
			return nil, fmt.Errorf("quota exceeded: bucket has quota of %d bytes, upload would exceed it", b.Quota)
		}
	}

	// Prepare the final payload (compress if needed)
	var toWrite []byte
	isCompressed := false
	if isCompressible(key, contentType) {
		enc, err := zstd.NewWriter(nil)
		if err != nil {
			return nil, err
		}
		toWrite = enc.EncodeAll(assembledPlaintext, nil)
		isCompressed = true
	} else {
		toWrite = assembledPlaintext
	}

	// Encrypt the complete assembled object before writing
	var finalPayload []byte
	if s.encryptionKey != nil {
		finalPayload, err = encryptPayload(s.encryptionKey, toWrite)
		if err != nil {
			return nil, err
		}
	} else {
		finalPayload = toWrite
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
		Checksum:       ParallelBlake3Hash(assembledPlaintext),
		Tags:           AutoClassify(key, contentType, assembledPlaintext),
		Compressed:     isCompressed,
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

func (s *LocalStore) PutSchema(ctx context.Context, service string, schema []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := []byte("s:schema:" + service)
	return s.pebbleDB.Set(key, schema, pebble.Sync)
}

func (s *LocalStore) GetSchema(ctx context.Context, service string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := []byte("s:schema:" + service)
	data, closer, err := s.pebbleDB.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer closer.Close()
	
	val := make([]byte, len(data))
	copy(val, data)
	return val, nil
}

func (s *LocalStore) ListSchemas(ctx context.Context) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	schemas := make(map[string][]byte)
	prefix := []byte("s:schema:")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid() && strings.HasPrefix(string(iter.Key()), "s:schema:"); iter.Next() {
		key := string(iter.Key())
		service := strings.TrimPrefix(key, "s:schema:")
		val := make([]byte, len(iter.Value()))
		copy(val, iter.Value())
		schemas[service] = val
	}
	return schemas, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	idx, err := s.getOrBuildHNSWIndexNoLock(bucket)
	if err != nil {
		return nil, err
	}

	queryVector := GenerateEmbedding(query)
	matchedNodes := idx.Search(queryVector, limit)

	var results []*ObjectVersion
	for _, node := range matchedNodes {
		om, err := s.readObjectMeta(bucket, node.Key)
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
		if latest != nil && !latest.IsDeleteMarker {
			results = append(results, latest)
		}
	}

	return results, nil
}

func (s *LocalStore) getOrBuildHNSWIndexNoLock(bucket string) (*HNSWIndex, error) {
	if s.hnswIndices == nil {
		s.hnswIndices = make(map[string]*HNSWIndex)
	}
	if idx, exists := s.hnswIndices[bucket]; exists {
		return idx, nil
	}

	idx := NewHNSWIndex()
	s.hnswIndices[bucket] = idx

	keys, err := s.scanMetadataKeys(bucket)
	if err != nil {
		return idx, nil
	}

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

		isText := strings.HasPrefix(latest.ContentType, "text/") || strings.Contains(key, ".txt") || strings.Contains(key, ".md")
		isImage := strings.HasPrefix(latest.ContentType, "image/") || strings.Contains(key, ".png") || strings.Contains(key, ".jpg") || strings.Contains(key, ".jpeg")
		isDoc := latest.ContentType == "application/pdf" || strings.Contains(key, ".pdf")
		isAudio := strings.HasPrefix(latest.ContentType, "audio/") || strings.Contains(key, ".mp3") || strings.Contains(key, ".wav")

		if isText || isImage || isDoc || isAudio {
			var embeddingSource string
			if isText {
				dataPath := s.getObjectDataPath(bucket, key, latest.VersionID)
				fileData, err := os.ReadFile(dataPath)
				if err != nil {
					continue
				}
				var plaintext []byte
				if s.encryptionKey != nil {
					plaintext, err = decryptPayload(s.encryptionKey, fileData)
					if err != nil {
						continue
					}
				} else {
					plaintext = fileData
				}
				embeddingSource = string(plaintext)
			} else if isImage {
				embeddingSource = "MOCK_CLIP_IMAGE_EMBEDDING_SOURCE: " + key
			} else if isDoc {
				embeddingSource = "MOCK_PDF_DOCUMENT_EMBEDDING_SOURCE: " + key
			} else if isAudio {
				embeddingSource = "MOCK_AUDIO_SPEECH_EMBEDDING_SOURCE: " + key
			}

			vector := GenerateEmbedding(embeddingSource)
			idx.Insert(key, vector)
		}
	}

	return idx, nil
}

func (s *LocalStore) updateHNSWIndexOnDeleteNoLock(bucket, key string, om *ObjectMeta) {
	idx, exists := s.hnswIndices[bucket]
	if !exists || idx == nil {
		return
	}

	var latest *ObjectVersion
	if om != nil {
		for i := range om.Versions {
			if om.Versions[i].IsLatest {
				latest = &om.Versions[i]
				break
			}
		}
		if latest == nil && len(om.Versions) > 0 {
			latest = &om.Versions[0]
		}
	}

	if latest == nil || latest.IsDeleteMarker {
		idx.mu.Lock()
		idx.deleteNodeNoLock(key)
		idx.mu.Unlock()
		return
	}

	if strings.HasPrefix(latest.ContentType, "text/") || strings.Contains(key, ".txt") || strings.Contains(key, ".md") {
		dataPath := s.getObjectDataPath(bucket, key, latest.VersionID)
		fileData, err := os.ReadFile(dataPath)
		if err != nil {
			idx.mu.Lock()
			idx.deleteNodeNoLock(key)
			idx.mu.Unlock()
			return
		}
		var plaintext []byte
		if s.encryptionKey != nil {
			var decErr error
			plaintext, decErr = decryptPayload(s.encryptionKey, fileData)
			if decErr != nil {
				idx.mu.Lock()
				idx.deleteNodeNoLock(key)
				idx.mu.Unlock()
				return
			}
		} else {
			plaintext = fileData
		}
		vector := GenerateEmbedding(string(plaintext))
		idx.Insert(key, vector)
	} else {
		idx.mu.Lock()
		idx.deleteNodeNoLock(key)
		idx.mu.Unlock()
	}
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




func (s *LocalStore) PutObjectTagging(ctx context.Context, bucket, key, versionID string, tags map[string]string) (ov *ObjectVersion, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil && ov != nil {
			_ = s.writeWAL(WALEntry{
				Timestamp: time.Now().UnixNano(),
				Operation: "PUT_TAGS",
				Bucket:    bucket,
				Key:       key,
				VersionID: versionID,
				Tags:      tags,
			})
		}
	}()

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	var targetVer *ObjectVersion
	for i := range om.Versions {
		if versionID == "" {
			if om.Versions[i].IsLatest {
				targetVer = &om.Versions[i]
				break
			}
		} else {
			if om.Versions[i].VersionID == versionID {
				targetVer = &om.Versions[i]
				break
			}
		}
	}

	if targetVer == nil {
		return nil, ErrObjectNotFound
	}

	targetVer.Tags = tags

	if err := s.writeObjectMeta(bucket, key, om); err != nil {
		return nil, err
	}

	return targetVer, nil
}

func (s *LocalStore) GetObjectTagging(ctx context.Context, bucket, key, versionID string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	var targetVer *ObjectVersion
	for i := range om.Versions {
		if versionID == "" {
			if om.Versions[i].IsLatest {
				targetVer = &om.Versions[i]
				break
			}
		} else {
			if om.Versions[i].VersionID == versionID {
				targetVer = &om.Versions[i]
				break
			}
		}
	}

	if targetVer == nil {
		return nil, ErrObjectNotFound
	}

	if targetVer.Tags == nil {
		return make(map[string]string), nil
	}

	return targetVer.Tags, nil
}

func (s *LocalStore) DeleteObjectTagging(ctx context.Context, bucket, key, versionID string) (ov *ObjectVersion, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err == nil && ctx.Value("is_restore") == nil && ov != nil {
			_ = s.writeWAL(WALEntry{
				Timestamp: time.Now().UnixNano(),
				Operation: "DELETE_TAGS",
				Bucket:    bucket,
				Key:       key,
				VersionID: versionID,
			})
		}
	}()

	om, err := s.readObjectMeta(bucket, key)
	if err != nil {
		return nil, err
	}

	var targetVer *ObjectVersion
	for i := range om.Versions {
		if versionID == "" {
			if om.Versions[i].IsLatest {
				targetVer = &om.Versions[i]
				break
			}
		} else {
			if om.Versions[i].VersionID == versionID {
				targetVer = &om.Versions[i]
				break
			}
		}
	}

	if targetVer == nil {
		return nil, ErrObjectNotFound
	}

	targetVer.Tags = nil

	if err := s.writeObjectMeta(bucket, key, om); err != nil {
		return nil, err
	}

	return targetVer, nil
}

func (s *LocalStore) SetBucketQuota(ctx context.Context, bucket string, quota int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.Quota = quota
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) GetBucketQuota(ctx context.Context, bucket string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return 0, err
	}

	return b.Quota, nil
}

func (s *LocalStore) SetBucketTriggers(ctx context.Context, bucket string, triggers []WASMTrigger) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.Triggers = triggers
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) GetBucketTriggers(ctx context.Context, bucket string) ([]WASMTrigger, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	if b.Triggers == nil {
		return []WASMTrigger{}, nil
	}
	return b.Triggers, nil
}

func (s *LocalStore) SetBucketNotifications(ctx context.Context, bucket string, rules []EventNotificationRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.NotificationConfig = rules
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) GetBucketNotifications(ctx context.Context, bucket string) ([]EventNotificationRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	if b.NotificationConfig == nil {
		return []EventNotificationRule{}, nil
	}
	return b.NotificationConfig, nil
}

func (s *LocalStore) SetBucketGeoPlacement(ctx context.Context, bucket string, cfg *GeoPlacementConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return err
	}

	b.GeoPlacement = cfg
	return s.writeBucketMeta(bucket, b)
}

func (s *LocalStore) GetBucketGeoPlacement(ctx context.Context, bucket string) (*GeoPlacementConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil, err
	}

	return b.GeoPlacement, nil
}


func (s *LocalStore) RestoreBucketToPointInTime(ctx context.Context, bucket string, targetTime time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	walPath := filepath.Join(s.rootDir, "backup.wal")
	f, err := os.Open(walPath)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()

	var entries []WALEntry
	dec := json.NewDecoder(f)
	for {
		var entry WALEntry
		if err := dec.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			break
		}
		entries = append(entries, entry)
	}

	targetNano := targetTime.UnixNano()

	// Clear local bucket files & metadata for clean replay
	_ = os.RemoveAll(s.getBucketDir(bucket))
	_ = s.pebbleDB.Delete([]byte("b:"+bucket), pebble.Sync)
	
	prefix := []byte("o:" + bucket + ":")
	iter, err := s.pebbleDB.NewIter(&pebble.IterOptions{LowerBound: prefix})
	if err == nil {
		var keysToDelete [][]byte
		for iter.First(); iter.Valid() && bytes.HasPrefix(iter.Key(), prefix); iter.Next() {
			keysToDelete = append(keysToDelete, append([]byte(nil), iter.Key()...))
		}
		iter.Close()
		for _, k := range keysToDelete {
			_ = s.pebbleDB.Delete(k, pebble.Sync)
		}
	}

	delete(s.hnswIndices, bucket)

	for _, entry := range entries {
		if entry.Bucket != bucket {
			continue
		}
		if entry.Timestamp > targetNano {
			break
		}

		switch entry.Operation {
		case "CREATE_BUCKET":
			bucketDir := s.getBucketDir(entry.Bucket)
			_ = os.MkdirAll(filepath.Join(bucketDir, ".data"), 0755)
			meta := Bucket{
				Name:        entry.Bucket,
				CreatedTime: time.Unix(0, entry.Timestamp),
				Versioning:  "Enabled",
			}
			_ = s.writeBucketMeta(entry.Bucket, &meta)

		case "DELETE_BUCKET":
			bucketDir := s.getBucketDir(entry.Bucket)
			_ = os.RemoveAll(bucketDir)
			_ = s.pebbleDB.Delete([]byte("b:"+entry.Bucket), pebble.Sync)

		case "PUT_OBJECT":
			dataPath := s.getObjectDataPath(entry.Bucket, entry.Key, entry.VersionID)
			_ = os.MkdirAll(filepath.Dir(dataPath), 0755)
			_ = os.WriteFile(dataPath, entry.Data, 0666)

			om, err := s.readObjectMeta(entry.Bucket, entry.Key)
			if err != nil {
				om = &ObjectMeta{Key: entry.Key, Versions: []ObjectVersion{}}
			}
			newVer := ObjectVersion{
				VersionID:    entry.VersionID,
				Key:          entry.Key,
				Size:         entry.Size,
				LastModified: time.Unix(0, entry.Timestamp),
				ContentType:  entry.ContentType,
				IsLatest:     true,
			}
			for i := range om.Versions {
				om.Versions[i].IsLatest = false
			}
			om.Versions = append([]ObjectVersion{newVer}, om.Versions...)
			_ = s.writeObjectMeta(entry.Bucket, entry.Key, om)

		case "DELETE_OBJECT":
			if entry.VersionID != "" {
				om, err := s.readObjectMeta(entry.Bucket, entry.Key)
				if err == nil {
					foundIndex := -1
					for i, ver := range om.Versions {
						if ver.VersionID == entry.VersionID {
							foundIndex = i
							break
						}
					}
					if foundIndex != -1 {
						dataPath := s.getObjectDataPath(entry.Bucket, entry.Key, entry.VersionID)
						_ = os.Remove(dataPath)
						om.Versions = append(om.Versions[:foundIndex], om.Versions[foundIndex+1:]...)
						if len(om.Versions) == 0 {
							_ = s.pebbleDB.Delete([]byte("o:"+entry.Bucket+":"+entry.Key), pebble.Sync)
						} else {
							om.Versions[0].IsLatest = true
							_ = s.writeObjectMeta(entry.Bucket, entry.Key, om)
						}
					}
				}
			} else {
				om, err := s.readObjectMeta(entry.Bucket, entry.Key)
				if err == nil {
					for i := range om.Versions {
						om.Versions[i].IsLatest = false
					}
					delMarker := ObjectVersion{
						VersionID:      generateUUID(),
						Key:            entry.Key,
						Size:           0,
						LastModified:   time.Unix(0, entry.Timestamp),
						IsLatest:       true,
						IsDeleteMarker: true,
					}
					om.Versions = append([]ObjectVersion{delMarker}, om.Versions...)
					_ = s.writeObjectMeta(entry.Bucket, entry.Key, om)
				}
			}

		case "PUT_TAGS":
			om, err := s.readObjectMeta(entry.Bucket, entry.Key)
			if err == nil {
				for i := range om.Versions {
					if (entry.VersionID == "" && om.Versions[i].IsLatest) || (entry.VersionID != "" && om.Versions[i].VersionID == entry.VersionID) {
						om.Versions[i].Tags = entry.Tags
						break
					}
				}
				_ = s.writeObjectMeta(entry.Bucket, entry.Key, om)
			}

		case "DELETE_TAGS":
			om, err := s.readObjectMeta(entry.Bucket, entry.Key)
			if err == nil {
				for i := range om.Versions {
					if (entry.VersionID == "" && om.Versions[i].IsLatest) || (entry.VersionID != "" && om.Versions[i].VersionID == entry.VersionID) {
						om.Versions[i].Tags = nil
						break
					}
				}
				_ = s.writeObjectMeta(entry.Bucket, entry.Key, om)
			}
		}
	}
	return nil
}


