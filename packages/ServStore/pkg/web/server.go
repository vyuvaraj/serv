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

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/cluster"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/metrics"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/otel"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"

	"github.com/vyuvaraj/serv/packages/ServShared"
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
	if strings.HasPrefix(path, "/api/v1/events/snapshots/") {
		wc.handleEventSnapshots(w, r)
		return
	}
	if path == "/healthz" {
		ServShared.HealthzHandler(w, r)
		return
	}
	if path == "/api/version" {
		ServShared.VersionHandler("github.com/vyuvaraj/serv/packages/ServStore", "1.0.0")(w, r)
		return
	}
	if path == "/readyz" {
		ServShared.ReadyzHandler(w, r)
		return
	}

	if path == "/console/ws/events" {
		if wc.auth.IsEnabled() {
			authenticated := wc.auth.VerifyRequest(r)
			if !authenticated {
				tokenQuery := r.URL.Query().Get("token")
				if tokenQuery != "" {
					_, err := auth.ValidateToken(tokenQuery, wc.auth.JWTSecret())
					if err == nil {
						authenticated = true
					}
				}
			}
			if !authenticated {
				WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
				return
			}
		}
		HandleWebSocketEvents(w, r)
		return
	}

	if strings.HasPrefix(path, "/api/v1/") {
		suffix := strings.TrimPrefix(path, "/api/v1/")
		if suffix == "schema" || suffix == "metrics" || suffix == "traces" || suffix == "presign" {
			path = "/console/" + suffix
		} else if strings.HasPrefix(suffix, "cluster/") {
			path = "/console/cluster/" + strings.TrimPrefix(suffix, "cluster/")
		} else if strings.HasPrefix(suffix, "users/") {
			path = "/console/users/" + strings.TrimPrefix(suffix, "users/")
		}
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
			WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}

		token, err := wc.auth.AuthenticateConsole(req.Username, req.Password)
		if err != nil {
			WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
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
				WriteJSONError(w, r, "OIDC auth URL generation failed: "+err.Error(), "ERR_OIDC_AUTH_URL_FAILED", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, authURL, http.StatusFound)
			return
		}
		WriteJSONError(w, r, "OIDC not configured", "ERR_OIDC_NOT_CONFIGURED", http.StatusBadRequest)
		return
	}

	if path == "/console/oauth/callback" {
		if oidc := wc.auth.OIDCClient(); oidc != nil && oidc.IsEnabled() {
			// Verify state
			stateCookie, err := r.Cookie("oidc_state")
			if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
				WriteJSONError(w, r, "Invalid state parameter", "ERR_INVALID_STATE", http.StatusBadRequest)
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
				WriteJSONError(w, r, "Missing code parameter", "ERR_MISSING_CODE", http.StatusBadRequest)
				return
			}

			tokenResp, err := oidc.ExchangeCode(code)
			if err != nil {
				WriteJSONError(w, r, "Token exchange failed: "+err.Error(), "ERR_TOKEN_EXCHANGE_FAILED", http.StatusInternalServerError)
				return
			}

			userInfo, err := oidc.GetUserInfo(tokenResp.AccessToken)
			if err != nil {
				WriteJSONError(w, r, "Failed to fetch user info: "+err.Error(), "ERR_USER_INFO_FAILED", http.StatusInternalServerError)
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
				Issuer:    "github.com/vyuvaraj/serv/packages/ServStore",
			}
			jwtToken, err := auth.GenerateToken(claims, wc.auth.JWTSecret())
			if err != nil {
				WriteJSONError(w, r, "Failed to generate token", "ERR_TOKEN_GENERATION_FAILED", http.StatusInternalServerError)
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
		WriteJSONError(w, r, "OIDC not configured", "ERR_OIDC_NOT_CONFIGURED", http.StatusBadRequest)
		return
	}

	if path == "/console/cluster/gossip" && r.Method == http.MethodPost {
		var payload cluster.GossipPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}
		if wc.cluster != nil {
			reply := wc.cluster.MergeGossip(payload)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(reply)
			return
		}
		WriteJSONError(w, r, "Cluster not enabled", "ERR_CLUSTER_NOT_ENABLED", http.StatusServiceUnavailable)
		return
	}

	if path == "/console/cluster/join" && r.Method == http.MethodPost {
		var req struct {
			NodeID  string `json:"node_id"`
			Address string `json:"address"`
			Region  string `json:"region"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
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
				WriteJSONError(w, r, "Join failed: "+err.Error(), "ERR_RAFT_JOIN_FAILED", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if wc.cluster != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		WriteJSONError(w, r, "Cluster not enabled", "ERR_CLUSTER_NOT_ENABLED", http.StatusServiceUnavailable)
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
			WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
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
			WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_REQUEST_BODY", http.StatusBadRequest)
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
			WriteJSONError(w, r, "Failed to generate presigned URL", "ERR_PRESIGNED_URL_FAILED", http.StatusInternalServerError)
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
				WriteJSONError(w, r, "Missing bucket or key parameter", "ERR_MISSING_PARAMETER", http.StatusBadRequest)
				return
			}
			owners, err := wc.cluster.Ring().GetNodes(bucket+"/"+key, 1)
			if err != nil || len(owners) == 0 {
				errMsg := "Key placement lookup failed"
				if err != nil {
					errMsg = err.Error()
				}
				WriteJSONError(w, r, errMsg, "ERR_PLACEMENT_LOOKUP_FAILED", http.StatusInternalServerError)
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
			WriteJSONError(w, r, "Cluster not enabled", "ERR_CLUSTER_NOT_ENABLED", http.StatusServiceUnavailable)
		}
		return
	}
	if path == "/console/schema" {
		switch r.Method {
		case http.MethodPost:
			service := r.URL.Query().Get("service")
			if service == "" {
				WriteJSONError(w, r, "Missing service parameter", "ERR_MISSING_PARAMETER", http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
				return
			}
			if err := wc.store.PutSchema(r.Context(), service, body); err != nil {
				WriteJSONError(w, r, err.Error(), "ERR_SCHEMA_PUT_FAILED", http.StatusInternalServerError)
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
						WriteJSONError(w, r, "Schema not found", "ERR_SCHEMA_NOT_FOUND", http.StatusNotFound)
						return
					}
					WriteJSONError(w, r, err.Error(), "ERR_SCHEMA_GET_FAILED", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			}
			schemas, err := wc.store.ListSchemas(r.Context())
			if err != nil {
				WriteJSONError(w, r, err.Error(), "ERR_SCHEMA_LIST_FAILED", http.StatusInternalServerError)
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
			WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
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
					WriteJSONError(w, r, err.Error(), "ERR_POLICY_GET_FAILED", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			case http.MethodPut:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
					return
				}
				var p auth.Policy
				if err := json.Unmarshal(body, &p); err != nil {
					WriteJSONError(w, r, "Invalid policy JSON: "+err.Error(), "ERR_INVALID_POLICY", http.StatusBadRequest)
					return
				}
				if wc.raftNode != nil {
					if !wc.raftNode.IsLeader() {
						WriteJSONError(w, r, "Not Raft leader. Propose to: "+wc.raftNode.LeaderAddr(), "ERR_NOT_LEADER", http.StatusBadRequest)
						return
					}
					cmd := cluster.MetadataCommand{
						Op:      "PutPolicy",
						KeyName: username,
						Value:   body,
					}
					if err := wc.raftNode.Propose(cmd); err != nil {
						WriteJSONError(w, r, err.Error(), "ERR_RAFT_PROPOSE_FAILED", http.StatusInternalServerError)
						return
					}
				} else {
					if err := wc.store.PutUserPolicy(r.Context(), username, body); err != nil {
						WriteJSONError(w, r, err.Error(), "ERR_POLICY_PUT_FAILED", http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusOK)
				return
			case http.MethodDelete:
				if wc.raftNode != nil {
					if !wc.raftNode.IsLeader() {
						WriteJSONError(w, r, "Not Raft leader. Propose to: "+wc.raftNode.LeaderAddr(), "ERR_NOT_LEADER", http.StatusBadRequest)
						return
					}
					cmd := cluster.MetadataCommand{
						Op:      "DeletePolicy",
						KeyName: username,
					}
					if err := wc.raftNode.Propose(cmd); err != nil {
						WriteJSONError(w, r, err.Error(), "ERR_RAFT_PROPOSE_FAILED", http.StatusInternalServerError)
						return
					}
				} else {
					if err := wc.store.DeleteUserPolicy(r.Context(), username); err != nil {
						WriteJSONError(w, r, err.Error(), "ERR_POLICY_DELETE_FAILED", http.StatusInternalServerError)
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
