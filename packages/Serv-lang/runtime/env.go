//go:build !wasm

package runtime

import (
	"fmt"
	"os"
	"strconv"
)

// EnvGet retrieves an environment variable or returns empty string if not set.
func EnvGet(key interface{}) interface{} {
	kStr := toString(key)
	return os.Getenv(kStr)
}

// EnvRequire retrieves an environment variable or panics if empty/unset.
func EnvRequire(key interface{}) interface{} {
	kStr := toString(key)
	val := os.Getenv(kStr)
	if val == "" {
		panic(fmt.Sprintf("Required environment variable %q is not set or is empty", kStr))
	}
	return val
}

// EnvInt retrieves and parses an environment variable as integer, returning default if unset or invalid.
func EnvInt(key interface{}, defaultValue interface{}) interface{} {
	kStr := toString(key)
	val := os.Getenv(kStr)
	if val == "" {
		return toFloat64(defaultValue)
	}
	parsed, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return toFloat64(defaultValue)
	}
	return float64(parsed)
}

// EnvBool retrieves and parses an environment variable as boolean, returning default if unset or invalid.
func EnvBool(key interface{}, defaultValue interface{}) interface{} {
	kStr := toString(key)
	val := os.Getenv(kStr)
	if val == "" {
		if b, ok := defaultValue.(bool); ok {
			return b
		}
		return false
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		if b, ok := defaultValue.(bool); ok {
			return b
		}
		return false
	}
	return parsed
}
