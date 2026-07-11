//go:build !enterprise

package storage

import "fmt"

// IsZeroCopySupported indicates if zero-copy serialization is supported.
const IsZeroCopySupported = false

func (w *WAL) writeRecord(data []byte) (int, error) {
	written, err := w.file.Write(data)
	if err != nil {
		return 0, fmt.Errorf("wal: append write failed: %w", err)
	}

	if err := w.file.Sync(); err != nil {
		return 0, fmt.Errorf("wal: append sync failed: %w", err)
	}

	return written, nil
}
