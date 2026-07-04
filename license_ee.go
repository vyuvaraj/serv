//go:build enterprise

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"strings"
	"time"
)

const licenseSalt = "servverse-license-secret-salt"

// verifyEnterpriseLicense checks the SERVVERSE_LICENSE_KEY environment variable.
// Key format: CompanyName:ExpirationDate:HMACSignatureHex
// E.g., Google:2027-01-01:ab12...
func verifyEnterpriseLicense() {
	key := os.Getenv("SERVVERSE_LICENSE_KEY")
	if key == "" {
		log.Fatalf("[ENTERPRISE ERROR] SERVVERSE_LICENSE_KEY environment variable is not set. A valid license key is required to start ServConsole Enterprise Edition.")
	}

	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		log.Fatalf("[ENTERPRISE ERROR] Invalid license key format. Expected format: CompanyName:ExpirationDate:Signature")
	}

	company := parts[0]
	expiryStr := parts[1]
	sigHex := parts[2]

	// Verify expiration format and date
	expiry, err := time.Parse("2006-01-02", expiryStr)
	if err != nil {
		log.Fatalf("[ENTERPRISE ERROR] Invalid expiration date format in license key: %v", err)
	}

	if time.Now().After(expiry) {
		log.Fatalf("[ENTERPRISE ERROR] The enterprise license key for %s expired on %s.", company, expiryStr)
	}

	// Verify cryptographic signature
	mac := hmac.New(sha256.New, []byte(licenseSalt))
	mac.Write([]byte(company + ":" + expiryStr))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if sigHex != expectedSig {
		log.Fatalf("[ENTERPRISE ERROR] Cryptographic verification failed. The license key signature is invalid.")
	}

	log.Printf("[ENTERPRISE] Valid license verified cryptographically for %s. Expiry: %s", company, expiryStr)
}
