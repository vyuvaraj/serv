package sessions

import (
	"sync"
	"time"

	"servauth/pkg/store"
)

var (
	Sessions   = make(map[string]*store.Session) // key: token
	SessionsMu sync.RWMutex

	EnterpriseRegisterAuthSession = func(token string, username string, ip string, userAgent string) error { return nil }
	EnterpriseVerifyAuthSession   = func(token string) bool { return true }
	EnterpriseRevokeAuthSession   = func(token string) error { return nil }

	// AI.32 stuffing detection variables
	failedLoginsIP   = make(map[string][]time.Time)
	FailedLoginsIPMu sync.Mutex
)

// IsSessionExpired checks if a session has expired (TTL of 24 hours)
func IsSessionExpired(s *store.Session) bool {
	return time.Since(s.CreatedAt) > 24*time.Hour
}

func RecordFailedLogin(ip string) {
	FailedLoginsIPMu.Lock()
	defer FailedLoginsIPMu.Unlock()
	failedLoginsIP[ip] = append(failedLoginsIP[ip], time.Now())
}

func GetStuffingIPs() []string {
	FailedLoginsIPMu.Lock()
	defer FailedLoginsIPMu.Unlock()

	stuffingIPs := []string{}
	now := time.Now()

	// Detect if any IP has had more than 3 failures in the last 60 seconds
	for ip, attempts := range failedLoginsIP {
		recent := 0
		for _, t := range attempts {
			if now.Sub(t) < 60*time.Second {
				recent++
			}
		}
		if recent >= 3 {
			stuffingIPs = append(stuffingIPs, ip)
		}
	}
	return stuffingIPs
}

func GetActiveSessions() []*store.Session {
	SessionsMu.RLock()
	defer SessionsMu.RUnlock()

	var list []*store.Session
	for _, s := range Sessions {
		if !s.Revoked && !IsSessionExpired(s) && EnterpriseVerifyAuthSession(s.Token) {
			list = append(list, s)
		}
	}
	return list
}
