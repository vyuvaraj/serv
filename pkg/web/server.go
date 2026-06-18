package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/cluster"
	"servstore/pkg/metrics"
	"servstore/pkg/otel"
	"servstore/pkg/storage"
)

//go:embed assets/*
var assetsFS embed.FS

type WebConsole struct {
	gateway    http.Handler
	fileServer http.Handler
	auth       *auth.AuthProvider
	store      storage.StorageEngine
	cluster    *cluster.MembershipManager
	raftNode   *cluster.RaftNode
}

func NewWebConsole(gateway http.Handler, authProvider *auth.AuthProvider, store storage.StorageEngine, clusterMgr *cluster.MembershipManager, raftNode *cluster.RaftNode) *WebConsole {
	// Strip assets prefix
	subFS, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}

	return &WebConsole{
		gateway:    gateway,
		fileServer: http.FileServer(http.FS(subFS)),
		auth:       authProvider,
		store:      store,
		cluster:    clusterMgr,
		raftNode:   raftNode,
	}
}

func (wc *WebConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/healthz" || path == "/readyz" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
		return
	}
	accept := r.Header.Get("Accept")

	// 1. Handle auth endpoints
	if path == "/console/auth-config" {
		w.Header().Set("Content-Type", "application/json")
		hasLDAP := false
		hasOIDC := false
		if wc.auth.IsEnabled() {
			hasLDAP = wc.auth.HasLDAP()
			hasOIDC = wc.auth.HasOIDC()
		}

		configMap := map[string]bool{
			"oidcEnabled": hasOIDC,
			"ldapEnabled": wc.auth.IsEnabled() || hasLDAP,
		}
		json.NewEncoder(w).Encode(configMap)
		return
	}

	if path == "/console/login" && r.Method == http.MethodPost {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		token, err := wc.auth.AuthenticateConsole(req.Username, req.Password)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Set cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400, // 24 hours
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token})
		return
	}

	if path == "/console/oauth/login" {
		if oidc := wc.auth.OIDCClient(); oidc != nil && oidc.IsEnabled() {
			// Generate state
			stateBytes := make([]byte, 16)
			rand.Read(stateBytes)
			state := hex.EncodeToString(stateBytes)

			// Store state in a cookie for validation
			http.SetCookie(w, &http.Cookie{
				Name:     "oidc_state",
				Value:    state,
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil,
				MaxAge:   300, // 5 minutes
			})

			authURL, err := oidc.GetAuthURL(state)
			if err != nil {
				http.Error(w, "OIDC auth URL generation failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, authURL, http.StatusFound)
			return
		}
		http.Error(w, "OIDC not configured", http.StatusBadRequest)
		return
	}

	if path == "/console/oauth/callback" {
		if oidc := wc.auth.OIDCClient(); oidc != nil && oidc.IsEnabled() {
			// Verify state
			stateCookie, err := r.Cookie("oidc_state")
			if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
				http.Error(w, "Invalid state parameter", http.StatusBadRequest)
				return
			}

			// Clear state cookie
			http.SetCookie(w, &http.Cookie{
				Name:   "oidc_state",
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})

			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "Missing code parameter", http.StatusBadRequest)
				return
			}

			tokenResp, err := oidc.ExchangeCode(code)
			if err != nil {
				http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			userInfo, err := oidc.GetUserInfo(tokenResp.AccessToken)
			if err != nil {
				http.Error(w, "Failed to fetch user info: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Extract username
			username, ok := userInfo["preferred_username"].(string)
			if !ok || username == "" {
				username, _ = userInfo["email"].(string)
			}
			if username == "" {
				username = "oidc-user"
			}

			// Generate local JWT token
			claims := auth.JWTClaims{
				Username:  username,
				Role:      "oidc-user",
				ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
				Issuer:    "servstore",
			}
			jwtToken, err := auth.GenerateToken(claims, wc.auth.JWTSecret())
			if err != nil {
				http.Error(w, "Failed to generate token", http.StatusInternalServerError)
				return
			}

			// Set token cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "token",
				Value:    jwtToken,
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400,
			})

			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.Error(w, "OIDC not configured", http.StatusBadRequest)
		return
	}

	if path == "/console/cluster/gossip" && r.Method == http.MethodPost {
		var payload cluster.GossipPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if wc.cluster != nil {
			reply := wc.cluster.MergeGossip(payload)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(reply)
			return
		}
		http.Error(w, "Cluster not enabled", http.StatusServiceUnavailable)
		return
	}

	if path == "/console/cluster/join" && r.Method == http.MethodPost {
		var req struct {
			NodeID  string `json:"node_id"`
			Address string `json:"address"`
			Region  string `json:"region"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		// If membership manager is active, register/gossip this new node
		if wc.cluster != nil {
			payload := cluster.GossipPayload{
				SourceNode: cluster.NodeInfo{
					NodeID:   req.NodeID,
					Address:  req.Address,
					Status:   "online",
					LastSeen: time.Now(),
					Region:   req.Region,
				},
				Peers: make(map[string]*cluster.NodeInfo),
			}
			wc.cluster.MergeGossip(payload)
		}
		if wc.raftNode != nil {
			if err := wc.raftNode.Join(req.NodeID, req.Address); err != nil {
				http.Error(w, "Join failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if wc.cluster != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "Cluster not enabled", http.StatusServiceUnavailable)
		return
	}

	if path == "/console/logout" {
		http.SetCookie(w, &http.Cookie{
			Name:     "token",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/login.html", http.StatusFound)
		return
	}

	// 2. Validate session/auth for console access
	if wc.auth.IsEnabled() {
		// Check if authenticated
		authenticated := wc.auth.VerifyRequest(r)

		// Allow public access to login page and public assets
		isPublicAsset := false
		if path == "/login.html" || path == "/style.css" || path == "/favicon.ico" || path == "/console/cluster/gossip" || path == "/console/cluster/join" {
			isPublicAsset = true
		}

		if !authenticated && !isPublicAsset {
			if strings.Contains(accept, "text/html") || path == "/" {
				http.Redirect(w, r, "/login.html", http.StatusFound)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Prevent authenticated users from going to login page
		if authenticated && path == "/login.html" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	// 2.5 Handle protected console endpoints
	if path == "/console/presign" && r.Method == http.MethodPost {
		var req struct {
			Method  string `json:"method"`
			Bucket  string `json:"bucket"`
			Key     string `json:"key"`
			Expires int    `json:"expires"` // seconds
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Method == "" {
			req.Method = "GET"
		}
		if req.Expires <= 0 {
			req.Expires = 3600 // default 1 hour
		}
		if req.Expires > 604800 {
			req.Expires = 604800 // max 7 days
		}
		// Determine base URL from request
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
		url, err := wc.auth.GeneratePresignedURL(baseURL, req.Method, req.Bucket, req.Key, time.Duration(req.Expires)*time.Second)
		if err != nil {
			http.Error(w, "Failed to generate presigned URL", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"url":     url,
			"method":  req.Method,
			"expires": req.Expires,
		})
		return
	}

	if path == "/console/cluster/status" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		if wc.cluster != nil {
			json.NewEncoder(w).Encode(wc.cluster.GetNodes())
		} else {
			w.Write([]byte(`[]`))
		}
		return
	}

	if path == "/console/metrics" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics.GetMetricsSnapshot())
		return
	}

	if path == "/console/traces" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(otel.GetRecentSpans())
		return
	}

	if path == "/console/cluster/ring" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		if wc.cluster != nil && wc.cluster.Ring() != nil {
			json.NewEncoder(w).Encode(wc.cluster.Ring().GetRingSnapshot())
		} else {
			w.Write([]byte(`{}`))
		}
		return
	}

	if path == "/console/cluster/placement" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		if wc.cluster != nil && wc.cluster.Ring() != nil {
			bucket := r.URL.Query().Get("bucket")
			key := r.URL.Query().Get("key")
			if bucket == "" || key == "" {
				http.Error(w, `{"error":"Missing bucket or key parameter"}`, http.StatusBadRequest)
				return
			}
			owners, err := wc.cluster.Ring().GetNodes(bucket+"/"+key, 1)
			if err != nil || len(owners) == 0 {
				errMsg := "Key placement lookup failed"
				if err != nil {
					errMsg = err.Error()
				}
				http.Error(w, fmt.Sprintf(`{"error":%q}`, errMsg), http.StatusInternalServerError)
				return
			}
			owner := owners[0]
			addr, _ := wc.cluster.GetNodeAddress(owner)
			res := map[string]string{
				"bucket":  bucket,
				"key":     key,
				"node_id": owner,
				"address": addr,
			}
			json.NewEncoder(w).Encode(res)
		} else {
			http.Error(w, `{"error":"Cluster not enabled"}`, http.StatusServiceUnavailable)
		}
		return
	}
	if path == "/console/schema" {
		switch r.Method {
		case http.MethodPost:
			service := r.URL.Query().Get("service")
			if service == "" {
				http.Error(w, `{"error":"Missing service parameter"}`, http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				return
			}
			if err := wc.store.PutSchema(r.Context(), service, body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
			return
		case http.MethodGet:
			service := r.URL.Query().Get("service")
			if service != "" {
				data, err := wc.store.GetSchema(r.Context(), service)
				if err != nil {
					if os.IsNotExist(err) {
						http.Error(w, `{"error":"Schema not found"}`, http.StatusNotFound)
						return
					}
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			}
			schemas, err := wc.store.ListSchemas(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			res := make(map[string]interface{})
			for k, v := range schemas {
				var jsonVal interface{}
				if err := json.Unmarshal(v, &jsonVal); err == nil {
					res[k] = jsonVal
				} else {
					res[k] = string(v)
				}
			}
			json.NewEncoder(w).Encode(res)
			return
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}

	if strings.HasPrefix(path, "/console/users/") && strings.HasSuffix(path, "/policy") {
		parts := strings.Split(path, "/")
		if len(parts) == 5 {
			username := parts[3]
			switch r.Method {
			case http.MethodGet:
				data, err := wc.store.GetUserPolicy(r.Context(), username)
				if err != nil {
					if os.IsNotExist(err) {
						w.Header().Set("Content-Type", "application/json")
						w.Write([]byte(`{"Statement":[]}`))
						return
					}
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			case http.MethodPut:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "Bad Request", http.StatusBadRequest)
					return
				}
				var p auth.Policy
				if err := json.Unmarshal(body, &p); err != nil {
					http.Error(w, "Invalid policy JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				if wc.raftNode != nil {
					if !wc.raftNode.IsLeader() {
						http.Error(w, "Not Raft leader. Propose to: "+wc.raftNode.LeaderAddr(), http.StatusBadRequest)
						return
					}
					cmd := cluster.MetadataCommand{
						Op:      "PutPolicy",
						KeyName: username,
						Value:   body,
					}
					if err := wc.raftNode.Propose(cmd); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				} else {
					if err := wc.store.PutUserPolicy(r.Context(), username, body); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusOK)
				return
			case http.MethodDelete:
				if wc.raftNode != nil {
					if !wc.raftNode.IsLeader() {
						http.Error(w, "Not Raft leader. Propose to: "+wc.raftNode.LeaderAddr(), http.StatusBadRequest)
						return
					}
					cmd := cluster.MetadataCommand{
						Op:      "DeletePolicy",
						KeyName: username,
					}
					if err := wc.raftNode.Propose(cmd); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				} else {
					if err := wc.store.DeleteUserPolicy(r.Context(), username); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}

	// 3. Serve standard assets or proxy to S3 Gateway
	isAsset := false
	if path == "/style.css" || path == "/app.js" || path == "/favicon.ico" || path == "/login.html" {
		isAsset = true
	} else if path == "/" && strings.Contains(accept, "text/html") {
		r.URL.Path = "/index.html"
		isAsset = true
	}

	if isAsset {
		wc.fileServer.ServeHTTP(w, r)
		return
	}

	wc.gateway.ServeHTTP(w, r)
}
