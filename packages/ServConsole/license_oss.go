//go:build !enterprise

package main

// verifyEnterpriseLicense is a no-op for the open-source version of ServConsole
func verifyEnterpriseLicense() {
	// No license check required in OSS
}
