package datafabric

import (
	"bytes"
	"testing"
)

func TestUniversalDataFabric(t *testing.T) {
	df := NewUniversalDataFabric()

	// 1. Test Put and Get Cache
	err := df.Put("cache://user_1", []byte("john"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	val, err := df.Get("cache://user_1")
	if err != nil || !bytes.Equal(val, []byte("john")) {
		t.Errorf("Get failed, val=%s, err=%v", string(val), err)
	}

	// 2. Test Put and Get Object Store
	err = df.Put("store://images/logo.png", []byte("pngdata"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	val, err = df.Get("store://images/logo.png")
	if err != nil || !bytes.Equal(val, []byte("pngdata")) {
		t.Errorf("Get failed, val=%s, err=%v", string(val), err)
	}

	// 3. Test Put and Get SQL Row
	err = df.Put("sql://users/1", []byte("sqldata"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	val, err = df.Get("sql://users/1")
	if err != nil || !bytes.Equal(val, []byte("sqldata")) {
		t.Errorf("Get failed, val=%s, err=%v", string(val), err)
	}

	// 4. Test Delete
	err = df.Delete("cache://user_1")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}
	_, err = df.Get("cache://user_1")
	if err == nil {
		t.Error("expected key to be deleted from cache")
	}

	// 5. Test invalid scheme
	err = df.Put("mongodb://db/coll", []byte("mongo"))
	if err == nil {
		t.Error("expected error for unsupported scheme")
	}
}
