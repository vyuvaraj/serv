package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	kmsKeys = map[string]string{
		"v1": "default-kms-secret-32-bytes-long!",
		"v2": "rotated-kms-secret-32-bytes-long!",
	}
	latestKMSKeyVersion = "v2"
	kmsMu               sync.RWMutex
)

func StartKMSRotationLoop(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		versionCounter := 2
		for range ticker.C {
			kmsMu.Lock()
			versionCounter++
			newVersion := fmt.Sprintf("v%d", versionCounter)

			// Generate a new 32-byte key dynamically
			newKeyBytes := make([]byte, 32)
			_, _ = io.ReadFull(rand.Reader, newKeyBytes)
			newKeyHex := hex.EncodeToString(newKeyBytes)

			kmsKeys[newVersion] = newKeyHex
			latestKMSKeyVersion = newVersion
			kmsMu.Unlock()
			log.Printf("[INFO] Rotated KMS envelope key to version %s", newVersion)
		}
	}()
}

func GetKMSKeyForVersion(version string) []byte {
	envKey := "SERV_KMS_SECRET_" + strings.ToUpper(version)
	secret := os.Getenv(envKey)
	if secret == "" {
		kmsMu.RLock()
		val, ok := kmsKeys[version]
		kmsMu.RUnlock()
		if ok {
			secret = val
		} else {
			secret = os.Getenv("SERV_KMS_SECRET")
			if secret == "" {
				secret = "default-kms-secret-32-bytes-long!"
			}
		}
	}
	key := make([]byte, 32)
	copy(key, []byte(secret))
	return key
}

// EncryptAES encrypts plaintext using AES-GCM with versioning
func EncryptAES(plaintext string) (string, error) {
	kmsMu.RLock()
	version := os.Getenv("SERV_KMS_SECRET_LATEST_VERSION")
	if version == "" {
		version = latestKMSKeyVersion
	}
	kmsMu.RUnlock()
	key := GetKMSKeyForVersion(version)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return version + ":" + hex.EncodeToString(ciphertext), nil
}

// DecryptAES decrypts versioned ciphertext using AES-GCM, with multi-version fallback.
func DecryptAES(prefixedCiphertext string) (string, error) {
	parts := strings.SplitN(prefixedCiphertext, ":", 2)
	var version string
	var hexCiphertext string
	if len(parts) == 2 {
		version = parts[0]
		hexCiphertext = parts[1]
	} else {
		// Legacy unversioned format fallback
		version = "v1"
		hexCiphertext = prefixedCiphertext
	}

	ciphertext, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", err
	}

	key := GetKMSKeyForVersion(version)
	plaintext, err := decryptWithKey(ciphertext, key)
	if err == nil {
		return plaintext, nil
	}

	kmsMu.RLock()
	activeVersions := make([]string, 0, len(kmsKeys))
	for k := range kmsKeys {
		if k != version {
			activeVersions = append(activeVersions, k)
		}
	}
	kmsMu.RUnlock()

	for _, v := range activeVersions {
		key = GetKMSKeyForVersion(v)
		plaintext, err = decryptWithKey(ciphertext, key)
		if err == nil {
			log.Printf("[INFO] Decrypted ciphertext with fallback KMS key version %s", v)
			return plaintext, nil
		}
	}

	return "", fmt.Errorf("decryption failed for all active KMS versions")
}

func decryptWithKey(ciphertext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
