package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

type OIDCClient struct {
	cfg OIDCConfig

	mu           sync.RWMutex
	authURL      string
	tokenURL     string
	userinfoURL  string
	discovered   bool
	discoveryErr error
}

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	return &OIDCClient{
		cfg: cfg,
	}
}

func (oc *OIDCClient) IsEnabled() bool {
	return oc.cfg.Issuer != "" && oc.cfg.ClientID != ""
}

func (oc *OIDCClient) discover() error {
	oc.mu.RLock()
	if oc.discovered {
		oc.mu.RUnlock()
		return oc.discoveryErr
	}
	oc.mu.RUnlock()

	oc.mu.Lock()
	defer oc.mu.Unlock()

	// Double check
	if oc.discovered {
		return oc.discoveryErr
	}

	issuer := strings.TrimSuffix(oc.cfg.Issuer, "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(discoveryURL)
	if err != nil {
		oc.discoveryErr = fmt.Errorf("failed to fetch OIDC discovery document: %w", err)
		oc.discovered = true
		return oc.discoveryErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		oc.discoveryErr = fmt.Errorf("OIDC discovery returned status %d", resp.StatusCode)
		oc.discovered = true
		return oc.discoveryErr
	}

	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		oc.discoveryErr = fmt.Errorf("failed to decode OIDC discovery JSON: %w", err)
		oc.discovered = true
		return oc.discoveryErr
	}

	oc.authURL = doc.AuthorizationEndpoint
	oc.tokenURL = doc.TokenEndpoint
	oc.userinfoURL = doc.UserinfoEndpoint
	oc.discovered = true
	oc.discoveryErr = nil
	return nil
}

// GetAuthURL returns the URL to redirect the user to for authentication.
func (oc *OIDCClient) GetAuthURL(state string) (string, error) {
	if err := oc.discover(); err != nil {
		return "", err
	}

	u, err := url.Parse(oc.authURL)
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Set("client_id", oc.cfg.ClientID)
	q.Set("redirect_uri", oc.cfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "openid profile email")
	q.Set("state", state)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// ExchangeCode exchanges the authorization code for tokens.
func (oc *OIDCClient) ExchangeCode(code string) (*TokenResponse, error) {
	if err := oc.discover(); err != nil {
		return nil, err
	}

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", oc.cfg.RedirectURI)
	data.Set("client_id", oc.cfg.ClientID)
	data.Set("client_secret", oc.cfg.ClientSecret)

	req, err := http.NewRequest("POST", oc.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange returned status %d", resp.StatusCode)
	}

	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}

	return &tr, nil
}

// GetUserInfo fetches the user profile using the access token.
func (oc *OIDCClient) GetUserInfo(accessToken string) (map[string]interface{}, error) {
	if err := oc.discover(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", oc.userinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo request returned status %d", resp.StatusCode)
	}

	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return info, nil
}
