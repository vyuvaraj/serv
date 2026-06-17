package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type LogEntry struct {
	Topic     string
	Payload   string
	Timestamp int64
}

type WAL struct {
	file *os.File
	mu   sync.Mutex
	path string
}

func OpenWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("wal: open failed: %w", err)
	}

	return &WAL{
		file: file,
		path: path,
	}, nil
}

func (w *WAL) Append(topic, payload string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	timestamp := time.Now().UnixNano()
	topicBytes := []byte(topic)
	payloadBytes := []byte(payload)

	// Binary frame layout:
	// [TopicLength (4B)][PayloadLength (4B)][Timestamp (8B)][TopicBytes][PayloadBytes][SHA256Checksum (32B)]
	header := make([]byte, 16)
	binary.BigEndian.PutUint32(header[0:4], uint32(len(topicBytes)))
	binary.BigEndian.PutUint32(header[4:8], uint32(len(payloadBytes)))
	binary.BigEndian.PutUint64(header[8:16], uint64(timestamp))

	hasher := sha256.New()
	hasher.Write(header)
	hasher.Write(topicBytes)
	hasher.Write(payloadBytes)
	checksum := hasher.Sum(nil)

	var frame bytesBuffer
	frame.Write(header)
	frame.Write(topicBytes)
	frame.Write(payloadBytes)
	frame.Write(checksum)

	if _, err := w.file.Write(frame.Bytes()); err != nil {
		return fmt.Errorf("wal: append write failed: %w", err)
	}

	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal: append sync failed: %w", err)
	}

	return nil
}

func (w *WAL) Recover() ([]LogEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var entries []LogEntry
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	header := make([]byte, 16)
	for {
		_, err := io.ReadFull(w.file, header)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}

		topicLen := binary.BigEndian.Uint32(header[0:4])
		payloadLen := binary.BigEndian.Uint32(header[4:8])
		timestamp := int64(binary.BigEndian.Uint64(header[8:16]))

		topicBytes := make([]byte, topicLen)
		if _, err := io.ReadFull(w.file, topicBytes); err != nil {
			return nil, err
		}

		payloadBytes := make([]byte, payloadLen)
		if _, err := io.ReadFull(w.file, payloadBytes); err != nil {
			return nil, err
		}

		checksum := make([]byte, 32)
		if _, err := io.ReadFull(w.file, checksum); err != nil {
			return nil, err
		}

		// Verify checksum
		hasher := sha256.New()
		hasher.Write(header)
		hasher.Write(topicBytes)
		hasher.Write(payloadBytes)
		computed := hasher.Sum(nil)

		for i := 0; i < 32; i++ {
			if checksum[i] != computed[i] {
				return nil, fmt.Errorf("wal: checksum verification failed - data corruption detected")
			}
		}

		entries = append(entries, LogEntry{
			Topic:     string(topicBytes),
			Payload:   string(payloadBytes),
			Timestamp: timestamp,
		})
	}

	return entries, nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

type bytesBuffer struct {
	buf []byte
}

func (b *bytesBuffer) Write(p []byte) {
	b.buf = append(b.buf, p...)
}

func (b *bytesBuffer) Bytes() []byte {
	return b.buf
}
