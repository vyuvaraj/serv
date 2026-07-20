package storage

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
)

// WALEntry represents a recorded transaction journal entry for S3 bucket/object mutations.
type WALEntry struct {
	Timestamp   int64             `json:"timestamp"`
	Operation   string            `json:"operation"`
	Bucket      string            `json:"bucket"`
	Key         string            `json:"key,omitempty"`
	VersionID   string            `json:"version_id,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Size        int64             `json:"size,omitempty"`
	Data        []byte            `json:"data,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// WriteWAL records the operation to a write-ahead log file, calling fsync to ensure durability.
func (s *LocalStore) writeWAL(entry WALEntry) error {
	walPath := filepath.Join(s.rootDir, "backup.wal")
	f, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}

	// Guarantee data durability via fsync
	return f.Sync()
}

// RecoverFromWAL replays all transactions in backup.wal to reconstruct store metadata.
func (s *LocalStore) RecoverFromWAL() error {
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
			log.Printf("[WAL RECOVERY] Error decoding transaction entry: %v", err)
			break
		}
		entries = append(entries, entry)
	}

	log.Printf("[WAL RECOVERY] Replaying %d transaction entries from %s...", len(entries), walPath)

	for _, entry := range entries {
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
				// Mark latest latest version as delete marker or clear it
				_ = s.pebbleDB.Delete([]byte("o:"+entry.Bucket+":"+entry.Key), pebble.Sync)
			}
		}
	}
	return nil
}
