//go:build !wasm

package runtime

import (
	"path/filepath"
)

// PathJoin joins any number of path elements into a single path.
func PathJoin(args ...interface{}) interface{} {
	parts := make([]string, len(args))
	for i, arg := range args {
		if s, ok := arg.(string); ok {
			parts[i] = s
		} else {
			parts[i] = ""
		}
	}
	return filepath.Join(parts...)
}

// PathDirname returns the directory part of the path.
func PathDirname(path string) interface{} {
	return filepath.Dir(path)
}

// PathBasename returns the base name of the path.
func PathBasename(path string) interface{} {
	return filepath.Base(path)
}

// PathExt returns the extension of the path.
func PathExt(path string) interface{} {
	return filepath.Ext(path)
}

// PathAbs returns the absolute representation of the path.
func PathAbs(path string) interface{} {
	abs, err := filepath.Abs(path)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return abs
}
