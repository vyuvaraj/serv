//go:build windows

package storage

import (
	"fmt"
	"os"
	"syscall"
)

const (
	// sectorSize is typical sector allocation block size (4KB)
	sectorSize = 4096
)

// WriteFileDirectIO writes data directly to disk bypassing the host OS page cache.
func WriteFileDirectIO(path string, data []byte) error {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	// 1. Allocate sector-aligned memory buffer for Direct I/O writes.
	alignedLength := (len(data) + sectorSize - 1) & ^(sectorSize - 1)
	alignedBuffer := make([]byte, alignedLength)
	copy(alignedBuffer, data)

	// Windows API CreateFile Call
	// FILE_FLAG_NO_BUFFERING = 0x20000000
	// FILE_FLAG_WRITE_THROUGH = 0x80000000
	flags := uint32(syscall.GENERIC_WRITE)
	shareMode := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE)
	creationDisposition := uint32(syscall.CREATE_ALWAYS)
	fileFlags := uint32(0x20000000 | 0x80000000)

	h, err := syscall.CreateFile(
		pathPtr,
		flags,
		shareMode,
		nil,
		creationDisposition,
		fileFlags,
		0,
	)
	if err != nil {
		return fmt.Errorf("CreateFile DirectIO failed: %w", err)
	}
	defer syscall.CloseHandle(h)

	// 2. Write aligned buffer to disk
	var bytesWritten uint32
	var overlapped syscall.Overlapped
	err = syscall.WriteFile(
		h,
		alignedBuffer,
		&bytesWritten,
		&overlapped,
	)
	if err != nil {
		return fmt.Errorf("WriteFile DirectIO failed: %w", err)
	}

	// 3. Truncate file to original unaligned size so it reads exactly correct
	if len(data) != alignedLength {
		// Re-open normally without DirectIO flags to truncate the file
		syscall.CloseHandle(h)
		f, err := os.OpenFile(path, os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Truncate(int64(len(data)))
	}

	return nil
}
