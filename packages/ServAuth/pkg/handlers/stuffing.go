//go:build enterprise

package handlers

// InitEnterprise registers the enterprise-only credential stuffing detector.
func InitEnterprise() {
	ActiveStuffingDetector = &eeStuffingDetector{}
}
