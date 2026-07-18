//go:build !wasm

package runtime

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
)

// CompressGzip compresses data (string or []byte) using gzip.
func CompressGzip(data interface{}) interface{} {
	var inputBytes []byte
	switch v := data.(type) {
	case string:
		inputBytes = []byte(v)
	case []byte:
		inputBytes = v
	default:
		inputBytes = []byte(toString(data))
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(inputBytes); err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	if err := zw.Close(); err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return buf.Bytes()
}

// CompressUngzip decompressed gzip bytes into a string.
func CompressUngzip(bytesVal interface{}) interface{} {
	var inputBytes []byte
	switch v := bytesVal.(type) {
	case []byte:
		inputBytes = v
	case string:
		inputBytes = []byte(v)
	default:
		return [2]interface{}{nil, "invalid input type for gzip decompression"}
	}

	zr, err := gzip.NewReader(bytes.NewReader(inputBytes))
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(out)
}

// CompressDeflate compresses data (string or []byte) using deflate.
func CompressDeflate(data interface{}) interface{} {
	var inputBytes []byte
	switch v := data.(type) {
	case string:
		inputBytes = []byte(v)
	case []byte:
		inputBytes = v
	default:
		inputBytes = []byte(toString(data))
	}

	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	if _, err := zw.Write(inputBytes); err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	if err := zw.Close(); err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return buf.Bytes()
}

// CompressInflate decompresses deflate bytes into a string.
func CompressInflate(bytesVal interface{}) interface{} {
	var inputBytes []byte
	switch v := bytesVal.(type) {
	case []byte:
		inputBytes = v
	case string:
		inputBytes = []byte(v)
	default:
		return [2]interface{}{nil, "invalid input type for flate decompression"}
	}

	zr := flate.NewReader(bytes.NewReader(inputBytes))
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return string(out)
}
