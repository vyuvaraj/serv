//go:build !wasm

package runtime

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
)

func HashMD5(str interface{}) interface{} {
	sStr := toString(str)
	h := md5.New()
	h.Write([]byte(sStr))
	return hex.EncodeToString(h.Sum(nil))
}

func HashSHA256(str interface{}) interface{} {
	sStr := toString(str)
	h := sha256.New()
	h.Write([]byte(sStr))
	return hex.EncodeToString(h.Sum(nil))
}

func HashSHA512(str interface{}) interface{} {
	sStr := toString(str)
	h := sha512.New()
	h.Write([]byte(sStr))
	return hex.EncodeToString(h.Sum(nil))
}

func HashHMAC(key interface{}, data interface{}, algo interface{}) interface{} {
	kStr := toString(key)
	dStr := toString(data)
	aStr := toString(algo)
	var h func() hash.Hash
	switch aStr {
	case "sha256", "SHA256":
		h = sha256.New
	case "sha512", "SHA512":
		h = sha512.New
	case "md5", "MD5":
		h = md5.New
	default:
		return [2]interface{}{nil, "unsupported HMAC algorithm: " + aStr}
	}
	mac := hmac.New(h, []byte(kStr))
	mac.Write([]byte(dStr))
	return hex.EncodeToString(mac.Sum(nil))
}
