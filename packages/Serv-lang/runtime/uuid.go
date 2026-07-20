//go:build !wasm

package runtime

import (
	"github.com/google/uuid"
)

// UUIDv4 generates a v4 UUID.
func UUIDv4() interface{} {
	return uuid.NewString()
}

// UUIDv7 generates a v7 UUID.
func UUIDv7() interface{} {
	u, err := uuid.NewV7()
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return u.String()
}
