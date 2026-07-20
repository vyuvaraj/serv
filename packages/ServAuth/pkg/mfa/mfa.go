package mfa

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"time"
)

func VerifyTOTP(secret string, code string) bool {
	var expectedCode int
	if _, err := fmt.Sscanf(code, "%d", &expectedCode); err != nil {
		return false
	}

	currentTime := time.Now().Unix()
	step := int64(30)
	key := []byte(secret)

	// Allow 1 step window for clock drift
	for i := -1; i <= 1; i++ {
		counter := (currentTime / step) + int64(i)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(counter))

		mac := hmac.New(sha1.New, key)
		mac.Write(buf)
		hs := mac.Sum(nil)

		offset := hs[len(hs)-1] & 0x0f
		binCode := int(hs[offset]&0x7f)<<24 |
			int(hs[offset+1]&0xff)<<16 |
			int(hs[offset+2]&0xff)<<8 |
			int(hs[offset+3]&0xff)

		otp := binCode % 1000000
		if otp == expectedCode {
			return true
		}
	}
	return false
}
