//go:build !wasm

package runtime

import (
	"crypto/rand"
	"encoding/binary"
	"math/big"
)

// toInt64 converts a generic interface{} number to int64.
func toInt64(val interface{}) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case int32:
		return int64(v)
	default:
		return 0
	}
}

// RandInt returns a secure random integer in [min, max].
func RandInt(min, max interface{}) interface{} {
	nMin := toInt64(min)
	nMax := toInt64(max)
	if nMax <= nMin {
		return nMin
	}
	diff := nMax - nMin + 1
	val, err := rand.Int(rand.Reader, big.NewInt(diff))
	if err != nil {
		return nMin
	}
	return val.Int64() + nMin
}

// RandFloat returns a secure random float64 in [0.0, 1.0).
func RandFloat() interface{} {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.0
	}
	val := binary.BigEndian.Uint64(b[:]) & ((1 << 53) - 1)
	return float64(val) / float64(1<<53)
}

// RandString returns a secure random alphanumeric string of length n.
func RandString(n interface{}) interface{} {
	length := int(toInt64(n))
	if length <= 0 {
		return ""
	}
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return ""
		}
		result[i] = chars[num.Int64()]
	}
	return string(result)
}

// RandBool returns a secure random boolean.
func RandBool() interface{} {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return false
	}
	return b[0]&1 == 1
}
