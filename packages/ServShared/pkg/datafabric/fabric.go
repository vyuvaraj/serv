package datafabric

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

type DataFabric interface {
	Get(uri string) ([]byte, error)
	Put(uri string, val []byte) error
	Delete(uri string) error
}

type UniversalDataFabric struct {
	mu          sync.RWMutex
	cacheStore  map[string][]byte
	objectStore map[string][]byte
	sqlStore    map[string][]byte
}

func NewUniversalDataFabric() *UniversalDataFabric {
	return &UniversalDataFabric{
		cacheStore:  make(map[string][]byte),
		objectStore: make(map[string][]byte),
		sqlStore:    make(map[string][]byte),
	}
}

func (df *UniversalDataFabric) parseURI(uri string) (scheme, path string, err error) {
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) < 2 {
		return "", "", errors.New("invalid URI: missing scheme")
	}
	return parts[0], parts[1], nil
}

func (df *UniversalDataFabric) Get(uri string) ([]byte, error) {
	scheme, path, err := df.parseURI(uri)
	if err != nil {
		return nil, err
	}

	df.mu.RLock()
	defer df.mu.RUnlock()

	switch scheme {
	case "cache":
		val, ok := df.cacheStore[path]
		if !ok {
			return nil, fmt.Errorf("key not found in cache: %s", path)
		}
		return val, nil
	case "store":
		val, ok := df.objectStore[path]
		if !ok {
			return nil, fmt.Errorf("object not found in store: %s", path)
		}
		return val, nil
	case "sql":
		val, ok := df.sqlStore[path]
		if !ok {
			return nil, fmt.Errorf("row not found in SQL database: %s", path)
		}
		return val, nil
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", scheme)
	}
}

func (df *UniversalDataFabric) Put(uri string, val []byte) error {
	scheme, path, err := df.parseURI(uri)
	if err != nil {
		return err
	}

	df.mu.Lock()
	defer df.mu.Unlock()

	switch scheme {
	case "cache":
		df.cacheStore[path] = val
		return nil
	case "store":
		df.objectStore[path] = val
		return nil
	case "sql":
		df.sqlStore[path] = val
		return nil
	default:
		return fmt.Errorf("unsupported scheme: %s", scheme)
	}
}

func (df *UniversalDataFabric) Delete(uri string) error {
	scheme, path, err := df.parseURI(uri)
	if err != nil {
		return err
	}

	df.mu.Lock()
	defer df.mu.Unlock()

	switch scheme {
	case "cache":
		if _, ok := df.cacheStore[path]; !ok {
			return fmt.Errorf("key not found in cache: %s", path)
		}
		delete(df.cacheStore, path)
		return nil
	case "store":
		if _, ok := df.objectStore[path]; !ok {
			return fmt.Errorf("object not found in store: %s", path)
		}
		delete(df.objectStore, path)
		return nil
	case "sql":
		if _, ok := df.sqlStore[path]; !ok {
			return fmt.Errorf("row not found in SQL database: %s", path)
		}
		delete(df.sqlStore, path)
		return nil
	default:
		return fmt.Errorf("unsupported scheme: %s", scheme)
	}
}
