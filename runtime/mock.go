package runtime

import (
	"sync"
)

var (
	activeMocks   = make(map[string]func(args ...interface{}) interface{})
	activeMocksMu sync.RWMutex
)

// RegisterMock registers a mock function callback for a key.
func RegisterMock(key string, mockFn func(args ...interface{}) interface{}) {
	activeMocksMu.Lock()
	defer activeMocksMu.Unlock()
	activeMocks[key] = mockFn
}

// ClearMocks clears all currently active mocks.
func ClearMocks() {
	activeMocksMu.Lock()
	defer activeMocksMu.Unlock()
	activeMocks = make(map[string]func(args ...interface{}) interface{})
}

// GetMock retrieves the active mock callback for a key, returning (fn, true) if found.
func GetMock(key string) (func(args ...interface{}) interface{}, bool) {
	activeMocksMu.RLock()
	defer activeMocksMu.RUnlock()
	fn, ok := activeMocks[key]
	return fn, ok
}
