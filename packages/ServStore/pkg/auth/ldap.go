package auth

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

type LDAPClient struct {
	URL        string // e.g. "ldap://localhost:389" or "ldaps://localhost:636"
	DNTemplate string // e.g. "cn=%s,ou=users,dc=servstore"
}

func NewLDAPClient(ldapURL, dnTemplate string) *LDAPClient {
	return &LDAPClient{
		URL:        ldapURL,
		DNTemplate: dnTemplate,
	}
}

// Authenticate attempts to bind to the LDAP server with the username and password.
func (lc *LDAPClient) Authenticate(username, password string) (bool, error) {
	if lc.URL == "" {
		return false, nil
	}

	u, err := url.Parse(lc.URL)
	if err != nil {
		return false, fmt.Errorf("invalid LDAP URL: %w", err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "ldaps" {
			host = host + ":636"
		} else {
			host = host + ":389"
		}
	}

	var conn *ldap.Conn
	if u.Scheme == "ldaps" {
		conn, err = ldap.DialTLS("tcp", host, &tls.Config{
			InsecureSkipVerify: true, // In production, we might want to verify this.
		})
	} else {
		conn, err = ldap.Dial("tcp", host)
	}

	if err != nil {
		return false, fmt.Errorf("failed to connect to LDAP: %w", err)
	}
	defer conn.Close()

	// Format Bind DN
	// E.g., if DNTemplate is "cn=%s,ou=users,dc=servstore", replace %s with username
	var bindDN string
	if strings.Contains(lc.DNTemplate, "%s") {
		bindDN = fmt.Sprintf(lc.DNTemplate, username)
	} else {
		bindDN = username // fallback to username directly if no placeholder
	}

	// Bind
	err = conn.Bind(bindDN, password)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
