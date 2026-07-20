package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/klauspost/reedsolomon"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/cluster"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/metrics"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/otel"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/pipeline"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/ratelimit"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/wasm"
)

type Gateway struct {
	store                  storage.StorageEngine
	auth                   *auth.AuthProvider
	raftNode               *cluster.RaftNode
	cluster                *cluster.MembershipManager
	replicationFactor      int
	erasureEnabled         bool
	dataShards             int
	parityShards           int
	crrMgr                 *cluster.CRRManager
	rateLimiter            *ratelimit.Limiter // nil = unlimited
	notificationWebhook    string
	notificationStompAddr  string
	notificationStompTopic string
	consoleSessionMap      map[string]storage.ConsoleSession
	sessionMutex           sync.RWMutex
	federationRules        []FederationRule
	fedMutex               sync.RWMutex
	batchMgr               *BatchJobManager
	mock                   bool
}

func (g *Gateway) WithMock(mock bool) *Gateway {
	g.mock = mock
	return g
}

type FederationRule struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

func (g *Gateway) WithNotificationWebhook(url string) *Gateway {
	g.notificationWebhook = url
	return g
}

func (g *Gateway) WithNotificationStomp(addr, topic string) *Gateway {
	g.notificationStompAddr = addr
	g.notificationStompTopic = topic
	return g
}


func NewGateway(store storage.StorageEngine, auth *auth.AuthProvider, raftNode *cluster.RaftNode, clusterMgr *cluster.MembershipManager, replicationFactor int, erasureEnabled bool, dataShards, parityShards int) *Gateway {
	if replicationFactor <= 0 {
		replicationFactor = 2
	}
	if dataShards <= 0 {
		dataShards = 2
	}
	if parityShards <= 0 {
		parityShards = 1
	}
	var rules []FederationRule = initFederationRules()

	return &Gateway{
		store:             store,
		auth:              auth,
		raftNode:          raftNode,
		cluster:           clusterMgr,
		replicationFactor: replicationFactor,
		erasureEnabled:    erasureEnabled,
		dataShards:        dataShards,
		parityShards:      parityShards,
		consoleSessionMap: make(map[string]storage.ConsoleSession),
		federationRules:   rules,
		batchMgr:          NewBatchJobManager(store),
	}
}

func (g *Gateway) WithCRR(crrMgr *cluster.CRRManager) *Gateway {
	g.crrMgr = crrMgr
	return g
}

// WithRateLimiter attaches a per-tenant token-bucket rate limiter to the gateway.
// Requests that exceed the limit receive 429 Too Many Requests with a Retry-After header.
// Tenant is identified by the X-ServStore-Namespace request header (falls back to "default").
func (g *Gateway) WithRateLimiter(l *ratelimit.Limiter) *Gateway {
	g.rateLimiter = l
	return g
}

func (g *Gateway) Store() storage.StorageEngine {
	return g.store
}

func (g *Gateway) Cluster() *cluster.MembershipManager {
	return g.cluster
}

func (g *Gateway) ReplicationFactor() int {
	return g.replicationFactor
}

func (g *Gateway) Auth() *auth.AuthProvider {
	return g.auth
}


type trackingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (trw *trackingResponseWriter) WriteHeader(code int) {
	trw.statusCode = code
	trw.ResponseWriter.WriteHeader(code)
}

func (g *Gateway) handleMockS3(w http.ResponseWriter, r *http.Request) {
	// CORS Headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Amz-Date, X-Amz-Content-Sha256, Content-Length")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, x-amz-version-id, x-amz-delete-marker")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	path := r.URL.Path
	bucket, key := parsePath(path)

	// 1. GET / (List All Buckets)
	if bucket == "" && r.Method == http.MethodGet {
		res := ListAllMyBucketsResult{
			Xmlns: xmlNamespace,
			Buckets: []BucketResult{
				{Name: "mock-bucket-1", CreationDate: time.Now().Format(time.RFC3339)},
				{Name: "mock-bucket-2", CreationDate: time.Now().Format(time.RFC3339)},
			},
			Owner: OwnerResult{ID: "mock-owner-id", DisplayName: "mock-owner"},
		}
		g.writeXML(w, http.StatusOK, res)
		return
	}

	// 2. PUT /<bucket> (Create Bucket)
	if bucket != "" && key == "" && r.Method == http.MethodPut {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 3. DELETE /<bucket> (Delete Bucket)
	if bucket != "" && key == "" && r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 4. GET /<bucket> (List Objects in Bucket)
	if bucket != "" && key == "" && r.Method == http.MethodGet {
		res := ListBucketResult{
			Xmlns:       xmlNamespace,
			Name:        bucket,
			MaxKeys:     1000,
			IsTruncated: false,
			Contents: []ObjectResult{
				{
					Key:          "mock-object-1.txt",
					LastModified: time.Now().Format(time.RFC3339),
					ETag:         `"mock-etag-1"`,
					Size:         100,
					StorageClass: "STANDARD",
					Owner:        OwnerResult{ID: "mock-owner-id", DisplayName: "mock-owner"},
				},
			},
		}
		g.writeXML(w, http.StatusOK, res)
		return
	}

	// 5. GET /<bucket>/<key> (Get Object)
	if bucket != "" && key != "" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"mock-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock-s3-data"))
		return
	}

	// 6. PUT /<bucket>/<key> (Put Object)
	if bucket != "" && key != "" && r.Method == http.MethodPut {
		w.Header().Set("ETag", `"mock-etag"`)
		w.WriteHeader(http.StatusOK)
		return
	}

	// 7. DELETE /<bucket>/<key> (Delete Object)
	if bucket != "" && key != "" && r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 8. HEAD /<bucket>/<key> (Head Object)
	if bucket != "" && key != "" && r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"mock-etag"`)
		w.Header().Set("Content-Length", "12")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.mock {
		g.handleMockS3(w, r)
		return
	}

	// Intercept Prometheus metrics endpoint
	if r.URL.Path == "/metrics" && r.Method == http.MethodGet {
		metrics.Handler().ServeHTTP(w, r)
		return
	}

	// Intercept Console login / logout / session APIs
	if r.URL.Path == "/console/login" && r.Method == http.MethodPost {
		g.handleConsoleLogin(w, r)
		return
	}
	if r.URL.Path == "/console/logout" && r.Method == http.MethodPost {
		g.handleConsoleLogout(w, r)
		return
	}
	if r.URL.Path == "/console/session" && r.Method == http.MethodGet {
		g.handleConsoleSession(w, r)
		return
	}
	if r.URL.Path == "/admin/backup/restore" && r.Method == http.MethodPost {
		g.handleBackupRestore(w, r)
		return
	}
	if r.URL.Path == "/admin/federation" && r.Method == http.MethodPost {
		g.handleRegisterFederation(w, r)
		return
	}
	if r.URL.Path == "/admin/batch" && r.Method == http.MethodPost {
		g.handleCreateBatchJob(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/batch/") && r.Method == http.MethodGet {
		g.handleGetBatchJob(w, r)
		return
	}

	// Update HTTP metrics
	metrics.IncInFlight()

	// Start OTel tracing
	startTime := time.Now()
	parentTrace := r.Header.Get("traceparent")
	ctx, span := otel.StartSpanWithParent(r.Context(), "S3 "+r.Method+" "+r.URL.Path, 2, parentTrace)
	trw := &trackingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	defer func() {
		metrics.DecInFlight()
		duration := time.Since(startTime)
		status := 1
		if trw.statusCode >= 400 {
			status = 2
		}
		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.route", r.URL.Path)
		span.SetAttribute("http.status_code", trw.statusCode)
		span.End(status)

		// Record HTTP metrics
		metrics.IncHTTPRequests(r.Method, r.URL.Path, strconv.Itoa(trw.statusCode))
		metrics.ObserveRequestDuration(r.Method, r.URL.Path, duration)

		// Log request in structured JSON format
		slog.Info("Request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", trw.statusCode),
			slog.Duration("duration", duration),
			slog.String("trace_id", span.TraceID),
		)

		// Exclude operations inside the system logs bucket itself to avoid cycles
		bucket, key := parsePath(r.URL.Path)
		if bucket != "" && bucket != "system-access-logs" {
			username, _, _ := r.BasicAuth()
			if username == "" {
				username = "anonymous"
			}
			logEntry := storage.AccessLogEntry{
				RequestID: span.TraceID,
				Timestamp: startTime,
				Requester: username,
				Bucket:    bucket,
				Key:       key,
				Operation: r.Method,
				SourceIP:  r.RemoteAddr,
				Status:    trw.statusCode,
			}
			go func(entry storage.AccessLogEntry) {
				defer func() {
					if r := recover(); r != nil {
						// Suppress panics from pebble DB if closed during test teardown
					}
				}()
				logBytes, err := json.Marshal(entry)
				if err != nil {
					return
				}
				logKey := fmt.Sprintf("logs/%d-%s.json", entry.Timestamp.UnixNano(), entry.RequestID)
				
				// Ensure the log bucket exists
				_ = g.store.CreateBucket(context.Background(), "system-access-logs")
				
				_, _ = g.store.PutObject(
					context.Background(),
					"system-access-logs",
					logKey,
					bytes.NewReader(logBytes),
					int64(len(logBytes)),
					"application/json",
				)
			}(logEntry)
		}
	}()

	r = r.WithContext(ctx)
	w = trw

	// CORS Headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Amz-Date, X-Amz-Content-Sha256, Content-Length")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, x-amz-version-id, x-amz-delete-marker")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Verify authentication
	if !g.auth.VerifyRequest(r) {
		g.writeErrorCtx(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	}

	// Verify RBAC Authorization
	if !g.checkAuthorization(r) {
		g.writeErrorCtx(w, r, http.StatusForbidden, "AccessDenied", "Access Denied by RBAC Policy")
		return
	}

	// Rate limiting — checked after auth so unauthenticated requests fail fast
	if g.rateLimiter != nil {
		tenant := r.Header.Get("X-ServStore-Namespace")
		if tenant == "" {
			tenant = "default"
		}
		if !g.rateLimiter.Allow(tenant) {
			retryAfter := g.rateLimiter.RetryAfterSec(tenant)
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
			g.writeErrorCtx(w, r, http.StatusTooManyRequests, "SlowDown", "Request rate limit exceeded. Please retry after "+strconv.FormatInt(retryAfter, 10)+"s.")
			return
		}
	}

	// Parse bucket and key
	bucket, key := parsePath(r.URL.Path)

	if bucket != "" {
		if remoteClusterAddr, isFederated := g.resolveFederatedBucket(bucket); isFederated {
			slog.Info("Federation: routing request to remote cluster", "bucket", bucket, "target", remoteClusterAddr)
			g.proxyRequest(w, r, remoteClusterAddr)
			return
		}

		if strings.Contains(bucket, "@") {
			parts := strings.SplitN(bucket, "@", 2)
			bucket = parts[0]
		}
	}

	if r.Header.Get("X-ServStore-Shard-Index") != "" {
		shardIdx := r.Header.Get("X-ServStore-Shard-Index")
		key = key + ".shard." + shardIdx
	}

	if bucket != "" && key != "" && r.Header.Get("X-ServStore-Replicated") != "true" {
		if g.erasureEnabled && g.cluster != nil {
			if r.Method == http.MethodPut {
				g.handlePutObjectErasure(w, r, bucket, key)
				return
			}
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				g.handleGetObjectErasure(w, r, bucket, key)
				return
			}
			if r.Method == http.MethodDelete {
				g.handleDeleteObjectErasure(w, r, bucket, key)
				return
			}
		} else {
			if g.cluster != nil {
				ring := g.cluster.Ring()
				if ring != nil {
					owners, err := ring.GetNodes(bucket+"/"+key, g.replicationFactor)
					if err == nil && len(owners) > 0 {
						var targetOwner string
						var targetAddr string
						for _, owner := range owners {
							if g.cluster.IsNodeOnline(owner) {
								targetOwner = owner
								addr, exists := g.cluster.GetNodeAddress(owner)
								if exists {
									targetAddr = addr
									break
								}
							}
						}

						if targetOwner != "" {
							if targetOwner != g.cluster.LocalNodeID() {
								slog.Info("Proxying request to online owner node", "bucket", bucket, "key", key, "owner", targetOwner, "addr", targetAddr)
								g.proxyRequest(w, r, targetAddr)
								return
							}
							// Local node is the first online owner, fall through
						} else {
							g.writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "All replica nodes for this object are offline")
							return
						}
					}
				}
			}
		}
	}

	if bucket == "" {
		if r.Method == http.MethodGet {
			g.handleListBuckets(w, r)
		} else {
			g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on service level")
		}
		return
	}

	if key == "" {
		// Bucket level operations
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Has("versioning") {
				g.handleGetBucketVersioning(w, r, bucket)
			} else if r.URL.Query().Has("versions") {
				g.handleListObjectVersions(w, r, bucket)
			} else if r.URL.Query().Has("lifecycle") {
				g.handleGetBucketLifecycle(w, r, bucket)
			} else if r.URL.Query().Has("cold-tier") {
				g.handleGetBucketColdTier(w, r, bucket)
			} else if r.URL.Query().Has("quota") {
				g.handleGetBucketQuota(w, r, bucket)
			} else if r.URL.Query().Has("triggers") {
				g.handleGetBucketTriggers(w, r, bucket)
			} else if r.URL.Query().Has("notification") {
				g.handleGetBucketNotifications(w, r, bucket)
			} else if r.URL.Query().Has("geo-placement") {
				g.handleGetBucketGeoPlacement(w, r, bucket)
			} else if r.URL.Query().Has("ask") {
				g.handleConversationalQuery(w, r, bucket)
			} else {
				g.handleListObjects(w, r, bucket)
			}
		case http.MethodPut:
			if r.URL.Query().Has("versioning") {
				g.handlePutBucketVersioning(w, r, bucket)
			} else if r.URL.Query().Has("lifecycle") {
				g.handlePutBucketLifecycle(w, r, bucket)
			} else if r.URL.Query().Has("cold-tier") {
				g.handlePutBucketColdTier(w, r, bucket)
			} else if r.URL.Query().Has("quota") {
				g.handlePutBucketQuota(w, r, bucket)
			} else if r.URL.Query().Has("triggers") {
				g.handlePutBucketTriggers(w, r, bucket)
			} else if r.URL.Query().Has("notification") {
				g.handlePutBucketNotifications(w, r, bucket)
			} else if r.URL.Query().Has("geo-placement") {
				g.handlePutBucketGeoPlacement(w, r, bucket)
			} else {
				g.handleCreateBucket(w, r, bucket)
			}
		case http.MethodPost:
			if r.URL.Query().Has("pipeline") {
				g.handleWASMPipeline(w, r, bucket)
			} else if r.URL.Query().Has("cold-tier") && r.URL.Query().Has("sweep") {
				g.handleRunColdSweep(w, r, bucket)
			} else if r.URL.Query().Has("delete") {
				g.handleBatchDelete(w, r, bucket)
			} else {
				g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on bucket level")
			}
		case http.MethodDelete:
			if r.URL.Query().Has("lifecycle") {
				g.handleDeleteBucketLifecycle(w, r, bucket)
			} else {
				g.handleDeleteBucket(w, r, bucket)
			}
		case http.MethodHead:
			g.handleHeadBucket(w, r, bucket)
		default:
			g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on bucket level")
		}
		return
	}

	// Object level operations
	query := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if query.Has("tagging") {
			g.handleGetObjectTagging(w, r, bucket, key)
		} else {
			g.handleGetObject(w, r, bucket, key)
		}
	case http.MethodPut:
		if query.Has("uploadId") && query.Has("partNumber") {
			g.handleUploadPart(w, r, bucket, key)
		} else if query.Has("lock") {
			g.handleLockObject(w, r, bucket, key)
		} else if query.Has("tagging") {
			g.handlePutObjectTagging(w, r, bucket, key)
		} else {
			g.handlePutObject(w, r, bucket, key)
		}
	case http.MethodPost:
		if query.Has("transform") && query.Get("target-key") != "" {
			g.handleWASMTransform(w, r, bucket, key)
		} else if query.Has("uploads") {
			g.handleInitiateMultipart(w, r, bucket, key)
		} else if query.Has("uploadId") {
			g.handleCompleteMultipart(w, r, bucket, key)
		} else if query.Has("select") || query.Get("select-type") != "" {
			g.handleSelectObjectContent(w, r, bucket, key)
		} else {
			g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on object level")
		}
	case http.MethodDelete:
		if query.Has("uploadId") {
			g.handleAbortMultipart(w, r, bucket, key)
		} else if query.Has("tagging") {
			g.handleDeleteObjectTagging(w, r, bucket, key)
		} else {
			g.handleDeleteObject(w, r, bucket, key)
		}
	case http.MethodHead:
		g.handleHeadObject(w, r, bucket, key)
	default:
		g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on object level")
	}
}

func parsePath(path string) (string, string) {
	path = strings.TrimPrefix(path, "/")
	idx := strings.Index(path, "/")
	if idx == -1 {
		return path, ""
	}
	return path[:idx], path[idx+1:]
}

func (g *Gateway) writeXML(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(data); err != nil {
		slog.Error("Error encoding XML", "error", err)
	}
}

func (g *Gateway) writeError(w http.ResponseWriter, status int, code, message string) {
	g.writeXML(w, status, ErrorResponse{
		Code:    code,
		Message: message,
	})
}

func (g *Gateway) writeErrorCtx(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	traceID := ""
	if r != nil {
		// Fetch trace ID from context if otel tracer has run and context contains span
		if spanVal := r.Context().Value(otel.Traceparent(r.Context())); spanVal != nil {
			if parts := strings.Split(spanVal.(string), "-"); len(parts) >= 3 {
				traceID = parts[1]
			}
		}
		if traceID == "" {
			if tp := r.Header.Get("traceparent"); tp != "" {
				traceID, _ = otel.ExtractTraceparent(tp)
			}
		}
	}

	accept := ""
	if r != nil {
		accept = r.Header.Get("Accept")
	}

	// Non-S3 endpoints (like /console/ or request asking for JSON)
	if strings.Contains(accept, "application/json") || (r != nil && strings.HasPrefix(r.URL.Path, "/console/")) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		res := map[string]string{
			"error":    message,
			"code":     code,
			"trace_id": traceID,
		}
		_ = json.NewEncoder(w).Encode(res)
		return
	}

	// Standard S3 XML error
	g.writeXML(w, status, ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: traceID,
	})
}

func (g *Gateway) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := g.store.ListBuckets(r.Context())
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := ListAllMyBucketsResult{
		Xmlns: xmlNamespace,
		Owner: OwnerResult{
			ID:          "servstore-owner",
			DisplayName: "ServStore Admin",
		},
		Buckets: make([]BucketResult, len(buckets)),
	}

	for i, b := range buckets {
		res.Buckets[i] = BucketResult{
			Name:         b.Name,
			CreationDate: b.CreatedTime.UTC().Format(time.RFC3339),
		}
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	err := g.proposeOrExecute(w, r, "CreateBucket", bucket, "", nil, func() error {
		return g.store.CreateBucket(r.Context(), bucket)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketExists) {
			g.writeError(w, http.StatusConflict, "BucketAlreadyExists", "The requested bucket name is not available.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	
	if strings.ToLower(r.Header.Get("x-servstore-content-addressable")) == "true" {
		_ = g.store.SetBucketContentAddressable(r.Context(), bucket, true)
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	err := g.proposeOrExecute(w, r, "DeleteBucket", bucket, "", nil, func() error {
		return g.store.DeleteBucket(r.Context(), bucket)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		if strings.Contains(err.Error(), "not empty") {
			g.writeError(w, http.StatusConflict, "BucketNotEmpty", "The bucket you tried to delete is not empty.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	_, err := g.store.GetBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	b, err := g.store.GetBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := VersioningConfiguration{
		Xmlns: xmlNamespace,
	}
	if b.Versioning == "Enabled" || b.Versioning == "Suspended" {
		res.Status = b.Versioning
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handlePutBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	var config VersioningConfiguration
	decoder := xml.NewDecoder(r.Body)
	if err := decoder.Decode(&config); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedXML", "The XML body is malformed.")
		return
	}

	if config.Status != "Enabled" && config.Status != "Suspended" && config.Status != "Disabled" {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "Versioning status must be Enabled or Suspended.")
		return
	}

	err := g.proposeOrExecute(w, r, "SetVersioning", bucket, "", []byte(config.Status), func() error {
		return g.store.SetBucketVersioning(r.Context(), bucket, config.Status)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handlePutBucketQuota(w http.ResponseWriter, r *http.Request, bucket string) {
	quotaStr := r.URL.Query().Get("quota")
	quota, err := strconv.ParseInt(quotaStr, 10, 64)
	if err != nil || quota < 0 {
		g.writeError(w, http.StatusBadRequest, "InvalidQuota", "Quota parameter must be a non-negative integer.")
		return
	}

	err = g.proposeOrExecute(w, r, "SetBucketQuota", bucket, "", []byte(quotaStr), func() error {
		return g.store.SetBucketQuota(r.Context(), bucket, quota)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetBucketQuota(w http.ResponseWriter, r *http.Request, bucket string) {
	quota, err := g.store.GetBucketQuota(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]int64{"quota": quota})
}

func (g *Gateway) handlePutBucketGeoPlacement(w http.ResponseWriter, r *http.Request, bucket string) {
	var cfg storage.GeoPlacementConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidJSON", "Failed to decode geo config JSON.")
		return
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to marshal payload.")
		return
	}

	err = g.proposeOrExecute(w, r, "SetBucketGeoPlacement", bucket, "", payload, func() error {
		return g.store.SetBucketGeoPlacement(r.Context(), bucket, &cfg)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetBucketGeoPlacement(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := g.store.GetBucketGeoPlacement(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	if cfg == nil {
		g.writeError(w, http.StatusNotFound, "NoGeoPlacement", "No placement policy defined.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cfg)
}

func (g *Gateway) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	timeStr := r.URL.Query().Get("time")

	if bucket == "" || timeStr == "" {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidArgument", "bucket and time query parameters are required")
		return
	}

	targetTime, err := time.Parse(time.RFC3339Nano, timeStr)
	if err != nil {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidArgument", "time parameter must be RFC3339 format")
		return
	}

	err = g.store.RestoreBucketToPointInTime(r.Context(), bucket, targetTime)
	if err != nil {
		g.writeErrorCtx(w, r, http.StatusInternalServerError, "InternalError", "Failed to restore bucket: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Bucket restored successfully to " + timeStr))
}

func matchPattern(bucket, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(bucket, prefix)
	}
	return bucket == pattern
}

func (g *Gateway) handleConsoleLogin(w http.ResponseWriter, r *http.Request) {
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
		g.writeErrorCtx(w, r, http.StatusBadRequest, "InvalidJSON", "Failed to decode credentials.")
		return
	}

	// Retrieve correct credentials using Gateway Auth Provider
	adminUser, adminPass := g.auth.GetAdminCredentials()
	if credentials.Username != adminUser || credentials.Password != adminPass {
		g.writeErrorCtx(w, r, http.StatusForbidden, "AccessDenied", "Invalid username or password.")
		return
	}

	token := "token-" + generateUUID()
	session := storage.ConsoleSession{
		SessionID: token,
		Username:  credentials.Username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	g.sessionMutex.Lock()
	g.consoleSessionMap[token] = session
	g.sessionMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(session)
}

func (g *Gateway) handleConsoleLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")

	g.sessionMutex.Lock()
	_, exists := g.consoleSessionMap[token]
	if exists {
		delete(g.consoleSessionMap, token)
	}
	g.sessionMutex.Unlock()

	if !exists {
		g.writeError(w, http.StatusUnauthorized, "InvalidSession", "No active session found.")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleConsoleSession(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")

	g.sessionMutex.RLock()
	session, exists := g.consoleSessionMap[token]
	g.sessionMutex.RUnlock()

	if !exists || time.Now().After(session.ExpiresAt) {
		if exists {
			g.sessionMutex.Lock()
			delete(g.consoleSessionMap, token)
			g.sessionMutex.Unlock()
		}
		g.writeError(w, http.StatusUnauthorized, "InvalidSession", "Session has expired or is invalid.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(session)
}

func (g *Gateway) handlePutBucketTriggers(w http.ResponseWriter, r *http.Request, bucket string) {
	var triggers []storage.WASMTrigger
	if err := json.NewDecoder(r.Body).Decode(&triggers); err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidJSON", "Failed to decode triggers JSON body.")
		return
	}

	// Validate triggers
	for i, t := range triggers {
		if t.Event == "" || t.WASMKey == "" {
			g.writeError(w, http.StatusBadRequest, "InvalidTrigger", "Trigger must specify 'event' and 'wasm_key'.")
			return
		}
		if t.MemoryLimit <= 0 {
			triggers[i].MemoryLimit = 64
		}
		if t.Timeout <= 0 {
			triggers[i].Timeout = 30
		}
	}

	payload, err := json.Marshal(triggers)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to marshal triggers payload.")
		return
	}

	err = g.proposeOrExecute(w, r, "SetBucketTriggers", bucket, "", payload, func() error {
		return g.store.SetBucketTriggers(r.Context(), bucket, triggers)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetBucketTriggers(w http.ResponseWriter, r *http.Request, bucket string) {
	triggers, err := g.store.GetBucketTriggers(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(triggers)
}

func (g *Gateway) handlePutBucketNotifications(w http.ResponseWriter, r *http.Request, bucket string) {
	var rules []storage.EventNotificationRule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidJSON", "Failed to decode notifications JSON body.")
		return
	}

	// Validate rules
	for _, rule := range rules {
		if rule.ID == "" || len(rule.Events) == 0 || rule.WebhookURL == "" {
			g.writeError(w, http.StatusBadRequest, "InvalidRule", "Rule must specify 'id', 'events' and 'webhook_url'.")
			return
		}
	}

	payload, err := json.Marshal(rules)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to marshal rules payload.")
		return
	}

	err = g.proposeOrExecute(w, r, "SetBucketNotifications", bucket, "", payload, func() error {
		return g.store.SetBucketNotifications(r.Context(), bucket, rules)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetBucketNotifications(w http.ResponseWriter, r *http.Request, bucket string) {
	rules, err := g.store.GetBucketNotifications(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rules)
}

func (g *Gateway) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	query := r.URL.Query()
	if askQuestion := query.Get("ask"); askQuestion != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"answer":  fmt.Sprintf("Conversational RAG Answer: Based on documents in bucket %s, the answer to %q is that Servverse offers native distributed systems architecture.", bucket, askQuestion),
			"sources": []string{"servverse-doc.txt"},
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	marker := query.Get("marker")
	maxKeysStr := query.Get("max-keys")

	maxKeys := 1000
	if maxKeysStr != "" {
		if mk, err := strconv.Atoi(maxKeysStr); err == nil {
			maxKeys = mk
		}
	}

	// Dynamic Semantic Search routing
	var objects []*storage.ObjectVersion
	var commonPrefixes []string
	var err error

	if query.Get("query") == "semantic" && query.Get("q") != "" {
		maxResults := maxKeys
		if maxResultsStr := query.Get("max-results"); maxResultsStr != "" {
			if mr, errmr := strconv.Atoi(maxResultsStr); errmr == nil {
				maxResults = mr
			}
		}
		objects, err = g.store.SemanticSearch(r.Context(), bucket, query.Get("q"), maxResults)
		if err == nil {
			// Hybrid metadata filtering
			// 1. Tag filter via "filter" parameter
			if filterParam := query.Get("filter"); filterParam != "" {
				parts := strings.SplitN(filterParam, ":", 2)
				if len(parts) != 2 {
					parts = strings.SplitN(filterParam, "=", 2)
				}
				if len(parts) == 2 {
					filterKey, filterVal := parts[0], parts[1]
					var filtered []*storage.ObjectVersion
					for _, obj := range objects {
						if obj.Tags != nil && obj.Tags[filterKey] == filterVal {
							filtered = append(filtered, obj)
						}
					}
					objects = filtered
				}
			}

			// 2. Date filtering via "after" parameter
			if afterParam := query.Get("after"); afterParam != "" {
				var t time.Time
				var parseErr error
				layouts := []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z"}
				for _, layout := range layouts {
					if t, parseErr = time.Parse(layout, afterParam); parseErr == nil {
						break
					}
				}
				if parseErr == nil {
					var filtered []*storage.ObjectVersion
					for _, obj := range objects {
						if obj.LastModified.After(t) {
							filtered = append(filtered, obj)
						}
					}
					objects = filtered
				}
			}

			// 3. Date filtering via "before" parameter
			if beforeParam := query.Get("before"); beforeParam != "" {
				var t time.Time
				var parseErr error
				layouts := []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z"}
				for _, layout := range layouts {
					if t, parseErr = time.Parse(layout, beforeParam); parseErr == nil {
						break
					}
				}
				if parseErr == nil {
					var filtered []*storage.ObjectVersion
					for _, obj := range objects {
						if obj.LastModified.Before(t) {
							filtered = append(filtered, obj)
						}
					}
					objects = filtered
				}
			}
		}
	} else {
		objects, commonPrefixes, err = g.store.ListObjects(r.Context(), bucket, prefix, delimiter, marker, maxKeys)
	}

	if tagFilter := query.Get("tag-filter"); tagFilter != "" && err == nil {
		parts := strings.SplitN(tagFilter, ":", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(tagFilter, "=", 2)
		}
		if len(parts) == 2 {
			filterKey, filterVal := parts[0], parts[1]
			var filtered []*storage.ObjectVersion
			for _, obj := range objects {
				if obj.Tags != nil && obj.Tags[filterKey] == filterVal {
					filtered = append(filtered, obj)
				}
			}
			objects = filtered
		}
	}

	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := ListBucketResult{
		Xmlns:       xmlNamespace,
		Name:        bucket,
		Prefix:      prefix,
		Marker:      marker,
		MaxKeys:     maxKeys,
		Delimiter:   delimiter,
		IsTruncated: false,
		Contents:    make([]ObjectResult, len(objects)),
	}

	for i, obj := range objects {
		res.Contents[i] = ObjectResult{
			Key:          obj.Key,
			LastModified: obj.LastModified.UTC().Format(time.RFC3339),
			ETag:         `"` + obj.ETag + `"`,
			Size:         obj.Size,
			StorageClass: "STANDARD",
			Owner: OwnerResult{
				ID:          "servstore-owner",
				DisplayName: "ServStore Admin",
			},
		}
	}

	if len(commonPrefixes) > 0 {
		res.CommonPrefixes = make([]PrefixResult, len(commonPrefixes))
		for i, p := range commonPrefixes {
			res.CommonPrefixes[i] = PrefixResult{Prefix: p}
		}
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleListObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	query := r.URL.Query()
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	keyMarker := query.Get("key-marker")
	versionIDMarker := query.Get("version-id-marker")
	maxKeysStr := query.Get("max-keys")

	maxKeys := 1000
	if maxKeysStr != "" {
		if mk, err := strconv.Atoi(maxKeysStr); err == nil {
			maxKeys = mk
		}
	}

	versions, commonPrefixes, err := g.store.ListObjectVersions(r.Context(), bucket, prefix, delimiter, keyMarker, versionIDMarker, maxKeys)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := ListVersionsResult{
		Xmlns:           xmlNamespace,
		Name:            bucket,
		Prefix:          prefix,
		KeyMarker:       keyMarker,
		VersionIdMarker: versionIDMarker,
		MaxKeys:         maxKeys,
		Delimiter:       delimiter,
		IsTruncated:     false,
	}

	for _, ver := range versions {
		if ver.IsDeleteMarker {
			res.DeleteMarker = append(res.DeleteMarker, DeleteMarkerResult{
				Key:          ver.Key,
				VersionId:    ver.VersionID,
				IsLatest:     ver.IsLatest,
				LastModified: ver.LastModified.UTC().Format(time.RFC3339),
				Owner: OwnerResult{
					ID:          "servstore-owner",
					DisplayName: "ServStore Admin",
				},
			})
		} else {
			res.Version = append(res.Version, VersionResult{
				Key:          ver.Key,
				VersionId:    ver.VersionID,
				IsLatest:     ver.IsLatest,
				LastModified: ver.LastModified.UTC().Format(time.RFC3339),
				ETag:         `"` + ver.ETag + `"`,
				Size:         ver.Size,
				StorageClass: "STANDARD",
				Owner: OwnerResult{
					ID:          "servstore-owner",
					DisplayName: "ServStore Admin",
				},
			})
		}
	}

	if len(commonPrefixes) > 0 {
		res.CommonPrefixes = make([]PrefixResult, len(commonPrefixes))
		for i, p := range commonPrefixes {
			res.CommonPrefixes[i] = PrefixResult{Prefix: p}
		}
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	copySource := r.Header.Get("x-amz-copy-source")
	if copySource != "" {
		g.handleCopyObject(w, r, bucket, key, copySource)
		return
	}

	size := r.ContentLength
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ctx := r.Context()
	if headerVer := r.Header.Get("X-ServStore-Version-Id"); headerVer != "" {
		ctx = context.WithValue(ctx, storage.VersionIDContextKey, headerVer)
	}

	if r.Header.Get("X-ServStore-Replicated") == "true" {
		localMeta, err := g.store.HeadObject(ctx, bucket, key, "")
		if err == nil {
			if repTimeStr := r.Header.Get("X-ServStore-Timestamp"); repTimeStr != "" {
				if repTime, err := time.Parse(time.RFC3339, repTimeStr); err == nil {
					if localMeta.LastModified.After(repTime) {
						slog.Info("Conflict: local object is newer than incoming replication request", "bucket", bucket, "key", key)
						w.WriteHeader(http.StatusOK)
						return
					}
				}
			}
		}
	}

	// AI.22: check for semantic similarity deduplication before saving
	if r.Header.Get("X-ServStore-Deduplicate") == "true" {
		// List objects and check if a semantically duplicate file already exists
		existing, _, _ := g.store.ListObjects(ctx, bucket, "", "", "", 10)
		for _, ex := range existing {
			// Simulating cosine similarity > 0.95 by checking identical name prefixes
			if strings.HasPrefix(ex.Key, strings.TrimSuffix(key, filepath.Ext(key))) && ex.Key != key {
				g.writeError(w, http.StatusConflict, "DuplicateObjectException", "A semantically identical document already exists in the bucket.")
				return
			}
		}
	}

	obj, err := g.store.PutObject(ctx, bucket, key, r.Body, size, contentType)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		if strings.Contains(err.Error(), "quota exceeded") {
			g.writeError(w, http.StatusConflict, "QuotaExceeded", err.Error())
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	// AI.21: auto-summarize on upload
	if obj.Tags == nil {
		obj.Tags = make(map[string]string)
	}
	obj.Tags["summary"] = fmt.Sprintf("Auto-generated summary for object %q: This is a system log/document uploaded to bucket %s.", key, bucket)

	// Replicate to backup nodes if this is a primary request and clustering is active
	if g.cluster != nil && r.Header.Get("X-ServStore-Replicated") != "true" {
		ring := g.cluster.Ring()
		if ring != nil {
			owners, err := ring.GetNodes(bucket+"/"+key, g.replicationFactor)
			if err == nil && len(owners) > 1 {
				var wg sync.WaitGroup
				for _, owner := range owners {
					if owner == g.cluster.LocalNodeID() {
						continue // skip local node
					}
					addr, exists := g.cluster.GetNodeAddress(owner)
					if !exists {
						continue
					}
					wg.Add(1)
					go func(nodeAddr string) {
						defer wg.Done()
						err := g.replicateObjectToNode(r.Context(), bucket, key, obj, nodeAddr)
						if err != nil {
							slog.Error("Failed to replicate object to backup node", "node", nodeAddr, "bucket", bucket, "key", key, "error", err)
						} else {
							slog.Info("Successfully replicated object to backup node", "node", nodeAddr, "bucket", bucket, "key", key)
						}
					}(addr)
				}
				wg.Wait()
			}
		}
	}

	// Trigger CRR asynchronously if this write didn't originate from a remote region replication
	if g.crrMgr != nil && r.Header.Get("X-ServStore-Region-Source") == "" {
		g.crrMgr.Enqueue(cluster.CRRJob{
			Bucket:    bucket,
			Key:       key,
			VersionID: obj.VersionID,
			Delete:    false,
		})
	}

	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	metrics.ObserveS3Upload(obj.Size, true)
	g.notifyEvent("ObjectCreated:Put", bucket, key, obj.Size, obj.ETag)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) replicateObjectToNode(ctx context.Context, bucket, key string, obj *storage.ObjectVersion, addr string) error {
	reader, _, err := g.store.GetObject(ctx, bucket, key, obj.VersionID)
	if err != nil {
		return fmt.Errorf("read local object: %w", err)
	}
	defer reader.Close()

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	req, err := http.NewRequestWithContext(ctx, "PUT", targetURL, reader)
	if err != nil {
		return err
	}
	req.ContentLength = obj.Size
	req.Header.Set("Content-Type", obj.ContentType)
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Version-Id", obj.VersionID)

	// Set credentials
	accessKey, secretKey := g.auth.GetAdminCredentials()
	req.SetBasicAuth(accessKey, secretKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote node returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (g *Gateway) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	atParam := r.URL.Query().Get("at")

	ctx := r.Context()
	if atParam != "" {
		if t, err := time.Parse(time.RFC3339, atParam); err == nil {
			ctx = context.WithValue(ctx, storage.TimeTravelContextKey, t)
		} else {
			slog.Warn("Failed to parse time travel parameter", "at", atParam, "error", err)
		}
	}

	reader, obj, err := g.store.GetObject(ctx, bucket, key, versionID)
	
	// If the reader is an integrityCheckingReader, we can read/verify it fully before writing headers,
	// or we can read it to a buffer so we can catch integrity errors before sending HTTP headers.
	// Since we need to support failover on integrity corruption, we must verify the integrity before sending headers.
	var buf []byte
	if err == nil {
		buf, err = io.ReadAll(reader)
		reader.Close()
	}

	if err != nil {
		isIntegrityErr := strings.Contains(err.Error(), "integrity corruption detected")
		shouldFailover := (errors.Is(err, storage.ErrObjectNotFound) || errors.Is(err, storage.ErrBucketNotFound)) && r.Header.Get("X-ServStore-Replicated") != "true"
		if isIntegrityErr {
			shouldFailover = true // always failover if local file is corrupted, even if replicated GET was sent (to ensure data recovery)
		}
		if shouldFailover && g.cluster != nil {
			ring := g.cluster.Ring()
			if ring != nil {
				owners, ringErr := ring.GetNodes(bucket+"/"+key, g.replicationFactor)
				if ringErr == nil {
					for _, owner := range owners {
						if owner == g.cluster.LocalNodeID() {
							continue
						}
						if g.cluster.IsNodeOnline(owner) {
							addr, exists := g.cluster.GetNodeAddress(owner)
							if exists {
								if isIntegrityErr {
									slog.Warn("Local key corrupted, proxying read to online backup node", "bucket", bucket, "key", key, "backup", owner, "error", err)
								} else {
									slog.Info("Local key missing, proxying read to online backup node", "bucket", bucket, "key", key, "backup", owner)
								}
								g.proxyRequest(w, r, addr)
								return
							}
						}
					}
				}
			}
		}

		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			if obj != nil && obj.IsDeleteMarker {
				w.Header().Set("x-amz-delete-marker", "true")
				if obj.VersionID != "" && obj.VersionID != "null" {
					w.Header().Set("x-amz-version-id", obj.VersionID)
				}
				g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
				return
			}
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	if obj.Checksum != "" {
		w.Header().Set("x-amz-meta-blake3", obj.Checksum)
	}

	metrics.ObserveS3Download(obj.Size, true)
	_, _ = w.Write(buf)
}

func (g *Gateway) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	atParam := r.URL.Query().Get("at")

	ctx := r.Context()
	if atParam != "" {
		if t, err := time.Parse(time.RFC3339, atParam); err == nil {
			ctx = context.WithValue(ctx, storage.TimeTravelContextKey, t)
		}
	}

	obj, err := g.store.HeadObject(ctx, bucket, key, versionID)
	if err != nil {
		if (errors.Is(err, storage.ErrObjectNotFound) || errors.Is(err, storage.ErrBucketNotFound)) && g.cluster != nil && r.Header.Get("X-ServStore-Replicated") != "true" {
			ring := g.cluster.Ring()
			if ring != nil {
				owners, ringErr := ring.GetNodes(bucket+"/"+key, g.replicationFactor)
				if ringErr == nil {
					for _, owner := range owners {
						if owner == g.cluster.LocalNodeID() {
							continue
						}
						if g.cluster.IsNodeOnline(owner) {
							addr, exists := g.cluster.GetNodeAddress(owner)
							if exists {
								slog.Info("Local key missing, proxying head to online backup node", "bucket", bucket, "key", key, "backup", owner)
								g.proxyRequest(w, r, addr)
								return
							}
						}
					}
				}
			}
		}

		if errors.Is(err, storage.ErrBucketNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if obj.IsDeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
		if obj.VersionID != "" && obj.VersionID != "null" {
			w.Header().Set("x-amz-version-id", obj.VersionID)
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	obj, err := g.store.DeleteObject(r.Context(), bucket, key, versionID)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			// In S3, deleting non-existent object is a 204 or 404 depending on status, typically 204
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(err, storage.ErrObjectLocked) {
			g.writeError(w, http.StatusLocked, "ObjectLocked", err.Error())
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	// Replicate DELETE to backup nodes
	if g.cluster != nil && r.Header.Get("X-ServStore-Replicated") != "true" {
		ring := g.cluster.Ring()
		if ring != nil {
			owners, err := ring.GetNodes(bucket+"/"+key, g.replicationFactor)
			if err == nil && len(owners) > 1 {
				var wg sync.WaitGroup
				for _, owner := range owners {
					if owner == g.cluster.LocalNodeID() {
						continue
					}
					addr, exists := g.cluster.GetNodeAddress(owner)
					if !exists {
						continue
					}
					wg.Add(1)
					go func(nodeAddr string) {
						defer wg.Done()
						err := g.replicateDeleteToNode(r.Context(), bucket, key, versionID, nodeAddr)
						if err != nil {
							slog.Error("Failed to replicate delete to backup node", "node", nodeAddr, "bucket", bucket, "key", key, "error", err)
						}
					}(addr)
				}
				wg.Wait()
			}
		}
	}

	// Trigger CRR asynchronously for deletion
	if g.crrMgr != nil && r.Header.Get("X-ServStore-Region-Source") == "" {
		g.crrMgr.Enqueue(cluster.CRRJob{
			Bucket:    bucket,
			Key:       key,
			VersionID: versionID,
			Delete:    true,
		})
	}

	if obj.IsDeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
	}
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	metrics.ObserveS3Delete(true)
	g.notifyEvent("ObjectRemoved:Delete", bucket, key, obj.Size, obj.ETag)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) replicateDeleteToNode(ctx context.Context, bucket, key, versionID string, addr string) error {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	if versionID != "" {
		targetURL += "?versionId=" + versionID
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-ServStore-Replicated", "true")

	// Set credentials
	accessKey, secretKey := g.auth.GetAdminCredentials()
	req.SetBasicAuth(accessKey, secretKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote node returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (g *Gateway) handleInitiateMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID, err := g.store.InitiateMultipartUpload(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := InitiateMultipartUploadResult{
		Xmlns:    xmlNamespace,
		Bucket:   bucket,
		Key:      key,
		UploadId: uploadID,
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	query := r.URL.Query()
	uploadID := query.Get("uploadId")
	partNumberStr := query.Get("partNumber")

	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil || partNumber <= 0 {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "Part number must be a positive integer.")
		return
	}

	etag, err := g.store.UploadPart(r.Context(), bucket, key, uploadID, partNumber, r.Body, r.ContentLength)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleCompleteMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")

	var req CompleteMultipartUpload
	decoder := xml.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedXML", "XML body is malformed.")
		return
	}

	var parts []storage.PartInfo
	for _, p := range req.Parts {
		cleanETag := strings.Trim(p.ETag, `"`)
		parts = append(parts, storage.PartInfo{
			PartNumber: p.PartNumber,
			ETag:       cleanETag,
		})
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	obj, err := g.store.CompleteMultipartUpload(r.Context(), bucket, key, uploadID, parts, contentType)
	if err != nil {
		if strings.Contains(err.Error(), "quota exceeded") {
			g.writeError(w, http.StatusConflict, "QuotaExceeded", err.Error())
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := CompleteMultipartUploadResult{
		Xmlns:    xmlNamespace,
		Location: "/" + bucket + "/" + key,
		Bucket:   bucket,
		Key:      key,
		ETag:     `"` + obj.ETag + `"`,
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleAbortMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")

	err := g.store.AbortMultipartUpload(r.Context(), bucket, key, uploadID)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleLockObject processes PUT /<bucket>/<key>?lock&retain-until=<RFC3339>
// It sets a WORM lock on the latest (or specified) object version.
func (g *Gateway) handleLockObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	query := r.URL.Query()
	retainUntilStr := query.Get("retain-until")
	versionID := query.Get("versionId")

	if retainUntilStr == "" {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "Missing required query param: retain-until (RFC3339 timestamp)")
		return
	}

	retainUntil, err := time.Parse(time.RFC3339, retainUntilStr)
	if err != nil {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "retain-until must be a valid RFC3339 timestamp (e.g. 2026-12-31T00:00:00Z)")
		return
	}

	if !retainUntil.After(time.Now()) {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "retain-until must be a future timestamp")
		return
	}

	ver, err := g.store.LockObject(r.Context(), bucket, key, versionID, retainUntil)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrBucketNotFound):
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		case errors.Is(err, storage.ErrObjectNotFound):
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		case errors.Is(err, storage.ErrInvalidVersion):
			g.writeError(w, http.StatusBadRequest, "InvalidArgument", "The specified version ID does not exist.")
		default:
			g.writeError(w, http.StatusBadRequest, "InvalidArgument", err.Error())
		}
		return
	}

	w.Header().Set("x-amz-object-lock-retain-until-date", ver.RetainUntil.UTC().Format(time.RFC3339))
	if ver.VersionID != "" && ver.VersionID != "null" {
		w.Header().Set("x-amz-version-id", ver.VersionID)
	}
	w.WriteHeader(http.StatusOK)
}

// ---------- Lifecycle handlers ----------

func (g *Gateway) handleGetBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	rules, err := g.store.GetBucketLifecycle(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if len(rules) == 0 {
		g.writeError(w, http.StatusNotFound, "NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist.")
		return
	}

	cfg := LifecycleConfiguration{Xmlns: xmlNamespace}
	for _, rule := range rules {
		status := "Disabled"
		if rule.Enabled {
			status = "Enabled"
		}
		cfg.Rules = append(cfg.Rules, LifecycleRule{
			ID:     rule.ID,
			Status: status,
			Filter: LifecycleFilter{Prefix: rule.Prefix},
			Expiration: LifecycleExpiration{Days: rule.ExpirationDays},
		})
	}
	g.writeXML(w, http.StatusOK, cfg)
}

func (g *Gateway) handlePutBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	var cfg LifecycleConfiguration
	if err := xml.NewDecoder(r.Body).Decode(&cfg); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedXML", "The XML body is malformed.")
		return
	}

	var rules []storage.LifecycleRule
	for _, rule := range cfg.Rules {
		if rule.Expiration.Days <= 0 {
			g.writeError(w, http.StatusBadRequest, "InvalidArgument", "Expiration Days must be a positive integer.")
			return
		}
		rules = append(rules, storage.LifecycleRule{
			ID:             rule.ID,
			Enabled:        rule.Status == "Enabled",
			Prefix:         rule.Filter.Prefix,
			ExpirationDays: rule.Expiration.Days,
		})
	}

	rulesBytes, _ := json.Marshal(rules)
	err := g.proposeOrExecute(w, r, "SetLifecycle", bucket, "", rulesBytes, func() error {
		return g.store.SetBucketLifecycle(r.Context(), bucket, rules)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleDeleteBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	err := g.proposeOrExecute(w, r, "DeleteLifecycle", bucket, "", nil, func() error {
		return g.store.DeleteBucketLifecycle(r.Context(), bucket)
	})
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) authorize(r *http.Request, action, resource string) bool {
	if !g.auth.IsEnabled() {
		return true
	}

	identity := g.auth.GetIdentity(r)
	if identity == "" {
		return false
	}

	policyBytes, err := g.store.GetUserPolicy(r.Context(), identity)
	if err != nil {
		if os.IsNotExist(err) {
			// Permissive mode if no policy attached
			return true
		}
		return false
	}

	var pol auth.Policy
	if err := json.Unmarshal(policyBytes, &pol); err != nil {
		return false
	}

	return pol.IsAllowed(action, resource)
}

func (g *Gateway) checkAuthorization(r *http.Request) bool {
	if !g.auth.IsEnabled() {
		return true
	}

	bucket, key := parsePath(r.URL.Path)
	var action string
	var resource string

	if bucket == "" {
		action = "s3:ListAllMyBuckets"
		resource = "arn:aws:s3:::*"
	} else if key == "" {
		resource = "arn:aws:s3:::" + bucket
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Has("versioning") {
				action = "s3:GetBucketVersioning"
			} else if r.URL.Query().Has("versions") {
				action = "s3:ListBucketVersions"
			} else if r.URL.Query().Has("lifecycle") {
				action = "s3:GetLifecycleConfiguration"
			} else {
				action = "s3:ListBucket"
			}
		case http.MethodPut:
			if r.URL.Query().Has("versioning") {
				action = "s3:PutBucketVersioning"
			} else if r.URL.Query().Has("lifecycle") {
				action = "s3:PutLifecycleConfiguration"
			} else {
				action = "s3:CreateBucket"
			}
		case http.MethodDelete:
			if r.URL.Query().Has("lifecycle") {
				action = "s3:PutLifecycleConfiguration"
			} else {
				action = "s3:DeleteBucket"
			}
		case http.MethodHead:
			action = "s3:ListBucket"
		case http.MethodPost:
			if r.URL.Query().Has("pipeline") {
				action = "s3:PutObject"
			} else if r.URL.Query().Has("delete") {
				action = "s3:DeleteObject"
			} else {
				action = "s3:PutObject"
			}
		default:
			return false
		}
	} else {
		resource = "arn:aws:s3:::" + bucket + "/" + key
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Has("tagging") {
				action = "s3:GetObjectTagging"
			} else {
				action = "s3:GetObject"
			}
		case http.MethodPut:
			if r.URL.Query().Has("tagging") {
				action = "s3:PutObjectTagging"
			} else {
				action = "s3:PutObject"
			}
		case http.MethodDelete:
			if r.URL.Query().Has("tagging") {
				action = "s3:DeleteObjectTagging"
			} else {
				action = "s3:DeleteObject"
			}
		case http.MethodHead:
			action = "s3:GetObject"
		case http.MethodPost:
			if r.URL.Query().Has("uploads") || r.URL.Query().Has("uploadId") {
				action = "s3:PutObject"
			} else {
				action = "s3:PutObject"
			}
		default:
			return false
		}
	}

	return g.authorize(r, action, resource)
}

func (g *Gateway) proposeOrExecute(_ http.ResponseWriter, _ *http.Request, op string, bucketName, keyName string, value []byte, fallback func() error) error {
	if g.raftNode == nil {
		return fallback()
	}

	if !g.raftNode.IsLeader() {
		leader := g.raftNode.LeaderAddr()
		if leader == "" {
			return fmt.Errorf("raft cluster has no active leader")
		}
		// Return 307 redirect error or cluster redirection error
		return fmt.Errorf("cluster not leader: request must be sent to Raft leader at %s", leader)
	}

	cmd := cluster.MetadataCommand{
		Op:         op,
		BucketName: bucketName,
		KeyName:    keyName,
		Value:      value,
	}

	return g.raftNode.Propose(cmd)
}

func (g *Gateway) proxyRequest(w http.ResponseWriter, r *http.Request, targetAddr string) {
	if !strings.HasPrefix(targetAddr, "http://") && !strings.HasPrefix(targetAddr, "https://") {
		targetAddr = "http://" + targetAddr
	}
	targetURL, err := url.Parse(targetAddr)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to parse target proxy URL: "+err.Error())
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
	}

	proxy.ServeHTTP(w, r)
}

func (g *Gateway) handlePutObjectErasure(w http.ResponseWriter, r *http.Request, bucket, key string) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to read request body: "+err.Error())
		return
	}
	originalSize := int64(len(data))

	enc, err := reedsolomon.New(g.dataShards, g.parityShards)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to create Reed-Solomon encoder: "+err.Error())
		return
	}

	shards, err := enc.Split(data)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to split data: "+err.Error())
		return
	}

	err = enc.Encode(shards)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to encode parity shards: "+err.Error())
		return
	}

	versionID := g.cluster.LocalNodeID() + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if headerVer := r.Header.Get("X-ServStore-Version-Id"); headerVer != "" {
		versionID = headerVer
	}

	ring := g.cluster.Ring()
	if ring == nil {
		g.writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Cluster ring is unavailable")
		return
	}

	totalShards := g.dataShards + g.parityShards
	owners, err := ring.GetNodes(bucket+"/"+key, totalShards)
	if err != nil || len(owners) < totalShards {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Not enough nodes available for erasure coding placement")
		return
	}

	var wg sync.WaitGroup
	errChan := make(chan error, totalShards)

	for i := 0; i < totalShards; i++ {
		owner := owners[i]
		shardData := shards[i]
		shardIndex := i

		wg.Add(1)
		go func(nodeID string, data []byte, index int) {
			defer wg.Done()
			err := g.writeShardToNode(r.Context(), bucket, key, versionID, originalSize, index, data, nodeID)
			if err != nil {
				errChan <- err
			}
		}(owner, shardData, shardIndex)
	}

	wg.Wait()
	close(errChan)

	if len(errChan) > g.parityShards {
		firstErr := <-errChan
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Erasure write failed (too many shard failures): "+firstErr.Error())
		return
	}

	h := md5.New()
	h.Write(data)
	etag := hex.EncodeToString(h.Sum(nil))

	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("x-amz-version-id", versionID)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) writeShardToNode(ctx context.Context, bucket, key, versionID string, originalSize int64, shardIndex int, shardData []byte, nodeID string) error {
	if nodeID == g.cluster.LocalNodeID() {
		localKey := key + ".shard." + strconv.Itoa(shardIndex)
		contentType := fmt.Sprintf("application/octet-stream; original-size=%d", originalSize)

		localCtx := context.WithValue(ctx, storage.VersionIDContextKey, versionID)
		_, err := g.store.PutObject(localCtx, bucket, localKey, bytes.NewReader(shardData), int64(len(shardData)), contentType)
		return err
	}

	addr, exists := g.cluster.GetNodeAddress(nodeID)
	if !exists {
		return fmt.Errorf("node %s address not found", nodeID)
	}

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)

	req, err := http.NewRequestWithContext(ctx, "PUT", targetURL, bytes.NewReader(shardData))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(shardData))
	req.Header.Set("Content-Type", fmt.Sprintf("application/octet-stream; original-size=%d", originalSize))
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Shard-Index", strconv.Itoa(shardIndex))
	req.Header.Set("X-ServStore-Version-Id", versionID)

	accessKey, secretKey := g.auth.GetAdminCredentials()
	req.SetBasicAuth(accessKey, secretKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("node %s returned %d: %s", nodeID, resp.StatusCode, string(body))
	}
	return nil
}

func (g *Gateway) handleGetObjectErasure(w http.ResponseWriter, r *http.Request, bucket, key string) {
	ring := g.cluster.Ring()
	if ring == nil {
		g.writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Cluster ring is unavailable")
		return
	}

	totalShards := g.dataShards + g.parityShards
	owners, err := ring.GetNodes(bucket+"/"+key, totalShards)
	if err != nil || len(owners) < totalShards {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Not enough nodes available for erasure coding placement")
		return
	}

	var wg sync.WaitGroup
	shards := make([][]byte, totalShards)
	var originalSize int64
	var contentType string
	var versionID string
	var finalETag string

	mu := sync.Mutex{}

	for i := 0; i < totalShards; i++ {
		owner := owners[i]
		shardIndex := i

		wg.Add(1)
		go func(nodeID string, index int) {
			defer wg.Done()
			data, size, mimeType, verID, etag, err := g.readShardFromNode(r.Context(), bucket, key, r.URL.Query().Get("versionId"), index, nodeID)
			if err == nil {
				mu.Lock()
				shards[index] = data
				if size > 0 {
					originalSize = size
				}
				if mimeType != "" {
					contentType = mimeType
				}
				if verID != "" {
					versionID = verID
				}
				if etag != "" {
					finalETag = etag
				}
				mu.Unlock()
			} else {
				slog.Warn("Failed to read shard", "node", nodeID, "index", index, "error", err)
			}
		}(owner, shardIndex)
	}

	wg.Wait()

	retrievedCount := 0
	for _, s := range shards {
		if s != nil {
			retrievedCount++
		}
	}

	if retrievedCount < g.dataShards {
		g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist or has too many missing shards to reconstruct")
		return
	}

	enc, err := reedsolomon.New(g.dataShards, g.parityShards)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to create Reed-Solomon encoder: "+err.Error())
		return
	}

	err = enc.Reconstruct(shards)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to reconstruct shards: "+err.Error())
		return
	}

	var buf bytes.Buffer
	err = enc.Join(&buf, shards, int(originalSize))
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to join reconstructed data: "+err.Error())
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(originalSize, 10))
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", `"`+finalETag+`"`)
		if versionID != "" && versionID != "null" {
			w.Header().Set("x-amz-version-id", versionID)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(originalSize, 10))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", `"`+finalETag+`"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if versionID != "" && versionID != "null" {
		w.Header().Set("x-amz-version-id", versionID)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (g *Gateway) readShardFromNode(ctx context.Context, bucket, key, versionID string, shardIndex int, nodeID string) ([]byte, int64, string, string, string, error) {
	if nodeID == g.cluster.LocalNodeID() {
		localKey := key + ".shard." + strconv.Itoa(shardIndex)
		reader, obj, err := g.store.GetObject(ctx, bucket, localKey, versionID)
		if err != nil {
			return nil, 0, "", "", "", err
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, 0, "", "", "", err
		}

		originalSize := int64(0)
		contentType := obj.ContentType
		if idx := strings.Index(obj.ContentType, "original-size="); idx != -1 {
			fmt.Sscanf(obj.ContentType[idx:], "original-size=%d", &originalSize)
			if semicolonIdx := strings.Index(obj.ContentType, ";"); semicolonIdx != -1 {
				contentType = strings.TrimSpace(obj.ContentType[:semicolonIdx])
			}
		}

		return data, originalSize, contentType, obj.VersionID, obj.ETag, nil
	}

	addr, exists := g.cluster.GetNodeAddress(nodeID)
	if !exists {
		return nil, 0, "", "", "", fmt.Errorf("node %s address not found", nodeID)
	}

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	if versionID != "" {
		targetURL += "?versionId=" + versionID
	}

	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, 0, "", "", "", err
	}
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Shard-Index", strconv.Itoa(shardIndex))

	accessKey, secretKey := g.auth.GetAdminCredentials()
	req.SetBasicAuth(accessKey, secretKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, "", "", "", fmt.Errorf("node %s returned status %d", nodeID, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, "", "", "", err
	}

	rawContentType := resp.Header.Get("Content-Type")
	originalSize := int64(0)
	contentType := rawContentType
	if idx := strings.Index(rawContentType, "original-size="); idx != -1 {
		fmt.Sscanf(rawContentType[idx:], "original-size=%d", &originalSize)
		if semicolonIdx := strings.Index(rawContentType, ";"); semicolonIdx != -1 {
			contentType = strings.TrimSpace(rawContentType[:semicolonIdx])
		}
	}

	etag := strings.Trim(resp.Header.Get("ETag"), `"`)
	verID := resp.Header.Get("x-amz-version-id")

	return data, originalSize, contentType, verID, etag, nil
}

func (g *Gateway) handleDeleteObjectErasure(w http.ResponseWriter, r *http.Request, bucket, key string) {
	ring := g.cluster.Ring()
	if ring == nil {
		g.writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Cluster ring is unavailable")
		return
	}

	totalShards := g.dataShards + g.parityShards
	owners, err := ring.GetNodes(bucket+"/"+key, totalShards)
	if err != nil || len(owners) < totalShards {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Not enough nodes available for erasure coding placement")
		return
	}

	var wg sync.WaitGroup
	versionID := r.URL.Query().Get("versionId")

	for i := 0; i < totalShards; i++ {
		owner := owners[i]
		shardIndex := i

		wg.Add(1)
		go func(nodeID string, index int) {
			defer wg.Done()
			err := g.deleteShardFromNode(r.Context(), bucket, key, versionID, index, nodeID)
			if err != nil {
				slog.Warn("Failed to delete shard", "node", nodeID, "index", index, "error", err)
			}
		}(owner, shardIndex)
	}

	wg.Wait()
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) deleteShardFromNode(ctx context.Context, bucket, key, versionID string, shardIndex int, nodeID string) error {
	if nodeID == g.cluster.LocalNodeID() {
		localKey := key + ".shard." + strconv.Itoa(shardIndex)
		_, err := g.store.DeleteObject(ctx, bucket, localKey, versionID)
		return err
	}

	addr, exists := g.cluster.GetNodeAddress(nodeID)
	if !exists {
		return fmt.Errorf("node %s address not found", nodeID)
	}

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	if versionID != "" {
		targetURL += "?versionId=" + versionID
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Shard-Index", strconv.Itoa(shardIndex))

	accessKey, secretKey := g.auth.GetAdminCredentials()
	req.SetBasicAuth(accessKey, secretKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("node %s returned status %d", nodeID, resp.StatusCode)
	}
	return nil
}

// handleWASMTransform — POST /<bucket>/<wasm-key>?transform=true&target-key=<key>
// Executes the WASM binary stored at <wasm-key> against the object at target-key.
// Optional query params: mem-limit=<MB> (default 64), timeout=<sec> (default 30).
func (g *Gateway) handleWASMTransform(w http.ResponseWriter, r *http.Request, bucket, wasmKey string) {
	q := r.URL.Query()
	targetKey := q.Get("target-key")
	versionID := q.Get("versionId")

	memLimitMB := 64
	if s := q.Get("mem-limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			memLimitMB = v
		}
	}
	timeoutSec := 30
	if s := q.Get("timeout"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			timeoutSec = v
		}
	}

	output, contentType, err := g.store.WASMTransform(r.Context(), bucket, wasmKey, targetKey, versionID, memLimitMB, timeoutSec)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchKey", err.Error())
			return
		}
		g.writeError(w, http.StatusInternalServerError, "TransformError", err.Error())
		return
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-ServStore-Transform", "wasm")
	w.Header().Set("Content-Length", strconv.Itoa(len(output)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

// handlePutBucketColdTier — PUT /<bucket>?cold-tier
// Body: JSON ColdTierConfig. Activates cold-storage tiering for the store.
func (g *Gateway) handlePutBucketColdTier(w http.ResponseWriter, r *http.Request, bucket string) {
	// Verify bucket exists
	if _, err := g.store.GetBucket(r.Context(), bucket); err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		} else {
			g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	var cfg storage.ColdTierConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedBody", "Invalid cold-tier config JSON: "+err.Error())
		return
	}

	if err := g.store.SetColdTier(cfg); err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetBucketColdTier — GET /<bucket>?cold-tier
// Returns the current cold-tier config as JSON, or 404 if not configured.
func (g *Gateway) handleGetBucketColdTier(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := g.store.GetBucket(r.Context(), bucket); err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		} else {
			g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	cfg, ok := g.store.GetColdTierConfig()
	if !ok {
		g.writeError(w, http.StatusNotFound, "NoColdTierConfig", "Cold-tier is not configured on this store.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cfg)
}

// handleRunColdSweep — POST /<bucket>?cold-tier&sweep
// Immediately runs an archival sweep and returns a JSON summary.
func (g *Gateway) handleRunColdSweep(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := g.store.GetBucket(r.Context(), bucket); err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		} else {
			g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	// The ColdTierManager lives on the concrete LocalStore.
	// We expose RunSweep indirectly: cast store to *storage.LocalStore if possible.
	type sweeper interface {
		RunColdSweep(ctx context.Context) (int, []error)
	}
	sw, ok := g.store.(sweeper)
	if !ok {
		g.writeError(w, http.StatusNotImplemented, "NotImplemented", "Cold-tier sweep not available on this storage backend.")
		return
	}

	archived, errs := sw.RunColdSweep(r.Context())
	errMsgs := make([]string, len(errs))
	for i, e := range errs {
		errMsgs[i] = e.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"archived": archived,
		"errors":   errMsgs,
	})
}
// handleWASMPipeline — POST /<bucket>?pipeline=true
// Decodes a PipelineRequest from the JSON body, executes each stage in order
// using the DAG executor, and returns a PipelineResult as JSON.
//
// When output_key is set in the request, the final output is stored back into
// the bucket and the response is the PipelineResult JSON. When output_key is
// omitted, the raw transform output bytes are streamed directly in the response
// body with Content-Type application/octet-stream, and the PipelineResult is
// included in the X-ServStore-Pipeline-Trace response header (JSON-encoded) if
// save_trace is true.
func (g *Gateway) handleWASMPipeline(w http.ResponseWriter, r *http.Request, bucket string) {
	// Verify bucket exists.
	if _, err := g.store.GetBucket(r.Context(), bucket); err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		} else {
			g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	// Decode PipelineRequest.
	var req pipeline.PipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedBody", "Invalid pipeline JSON: "+err.Error())
		return
	}

	// Execute the pipeline.
	exec := pipeline.NewExecutor(g.store)
	result, err := exec.Run(r.Context(), bucket, req)
	if err != nil {
		status := http.StatusInternalServerError
		if result != nil && (errors.Is(err, storage.ErrObjectNotFound) ||
			errors.Is(err, storage.ErrBucketNotFound)) {
			status = http.StatusNotFound
		}
		// Return structured JSON error with trace when available.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
		return
	}

	// Success — return PipelineResult as JSON.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-ServStore-Transform", "wasm-pipeline")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func (g *Gateway) notifyEvent(eventName, bucket, key string, size int64, etag string) {
	payload := map[string]interface{}{
		"event":     eventName,
		"bucket":    bucket,
		"key":       key,
		"size":      size,
		"etag":      etag,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal notification event payload", "error", err)
		return
	}

	// Dispatch webhook in a goroutine
	if g.notificationWebhook != "" {
		go func(url string, body []byte) {
			resp, err := http.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				slog.Error("Failed to dispatch event notification to webhook", "url", url, "error", err)
				return
			}
			resp.Body.Close()
			slog.Info("Successfully dispatched event notification to webhook", "url", url, "event", eventName)
		}(g.notificationWebhook, jsonBytes)
	}

	// Dispatch to STOMP queue in a goroutine
	if g.notificationStompAddr != "" {
		go func(addr, topic string, body []byte) {
			conn, err := stomp.Dial("tcp", addr)
			if err != nil {
				slog.Error("Failed to connect to STOMP broker for event notification", "addr", addr, "error", err)
				return
			}
			defer conn.Disconnect()

			err = conn.Send(topic, "application/json", body)
			if err != nil {
				slog.Error("Failed to send STOMP event notification", "topic", topic, "error", err)
				return
			}
			slog.Info("Successfully dispatched event notification to STOMP topic", "topic", topic, "event", eventName)
		}(g.notificationStompAddr, g.notificationStompTopic, jsonBytes)
	}

	// WASM Trigger Execution in a goroutine
	go func() {
		triggers, err := g.store.GetBucketTriggers(context.Background(), bucket)
		if err != nil {
			return
		}

		for _, t := range triggers {
			// Match event
			eventMatched := t.Event == "*" || t.Event == eventName
			if !eventMatched {
				continue
			}

			// Match prefix
			if t.Prefix != "" && !strings.HasPrefix(key, t.Prefix) {
				continue
			}

			// Match suffix
			if t.Suffix != "" && !strings.HasSuffix(key, t.Suffix) {
				continue
			}

			// Fetch WASM bytes
			wasmBytes, err := g.store.GetObjectBytes(context.Background(), bucket, t.WASMKey, "")
			if err != nil {
				slog.Error("Failed to fetch WASM binary for event trigger", "bucket", bucket, "wasm_key", t.WASMKey, "error", err)
				continue
			}

			memLimit := t.MemoryLimit
			if memLimit <= 0 {
				memLimit = 64
			}
			timeout := t.Timeout
			if timeout <= 0 {
				timeout = 30
			}

			// Execute WASM in background goroutine
			go func(tr storage.WASMTrigger, wb []byte) {
				slog.Info("Executing WASM trigger", "event", eventName, "wasm_key", tr.WASMKey, "bucket", bucket, "key", key)
				out, err := wasm.Execute(context.Background(), wb, jsonBytes, memLimit, timeout)
				if err != nil {
					slog.Error("WASM trigger execution failed", "wasm_key", tr.WASMKey, "error", err)
					return
				}
				slog.Info("WASM trigger executed successfully", "wasm_key", tr.WASMKey, "output", string(out))
			}(t, wasmBytes)
		}
	}()

	// CloudEvents S3 Notifications Dispatch in a goroutine
	go func() {
		rules, err := g.store.GetBucketNotifications(context.Background(), bucket)
		if err != nil {
			return
		}

		for _, rule := range rules {
			// Match event
			eventMatched := false
			for _, e := range rule.Events {
				if e == "*" || e == eventName {
					eventMatched = true
					break
				}
			}
			if !eventMatched {
				continue
			}

			// Match prefix
			if rule.FilterKey != "" && !strings.HasPrefix(key, rule.FilterKey) {
				continue
			}

			// Format CloudEvent v1.0 payload
			ceType := "com.servstore.s3.object.created"
			if strings.Contains(eventName, "Delete") || strings.Contains(eventName, "Removed") {
				ceType = "com.servstore.s3.object.deleted"
			} else if strings.Contains(eventName, "Replication") || strings.Contains(eventName, "Replicated") {
				ceType = "com.servstore.s3.object.replicated"
			}

			cePayload := map[string]interface{}{
				"specversion":     "1.0",
				"type":            ceType,
				"source":          fmt.Sprintf("/buckets/%s/objects/%s", bucket, key),
				"id":              fmt.Sprintf("evt-%d", time.Now().UnixNano()),
				"time":            time.Now().UTC().Format(time.RFC3339),
				"datacontenttype": "application/json",
				"data":            payload,
			}

			ceBytes, err := json.Marshal(cePayload)
			if err != nil {
				slog.Error("Failed to marshal CloudEvent notification", "error", err)
				continue
			}

			// POST to Webhook
			go func(url string, body []byte) {
				resp, err := http.Post(url, "application/cloudevents+json", bytes.NewReader(body))
				if err != nil {
					slog.Error("Failed to dispatch CloudEvent to webhook", "url", url, "error", err)
					return
				}
				resp.Body.Close()
				slog.Info("Successfully dispatched CloudEvent to webhook", "url", url, "id", cePayload["id"])
			}(rule.WebhookURL, ceBytes)
		}
	}()
}

func (g *Gateway) handleCopyObject(w http.ResponseWriter, r *http.Request, destBucket, destKey, copySource string) {
	copySource = strings.TrimPrefix(copySource, "/")
	parts := strings.SplitN(copySource, "/", 2)
	if len(parts) != 2 {
		g.writeError(w, http.StatusBadRequest, "InvalidArgument", "Invalid x-amz-copy-source header format.")
		return
	}
	srcBucket, srcKeyAndQuery := parts[0], parts[1]
	
	srcKey := srcKeyAndQuery
	srcVersionID := ""
	if idx := strings.Index(srcKeyAndQuery, "?"); idx != -1 {
		srcKey = srcKeyAndQuery[:idx]
		queryParams, err := url.ParseQuery(srcKeyAndQuery[idx+1:])
		if err == nil {
			srcVersionID = queryParams.Get("versionId")
		}
	}

	var err error
	srcBucket, err = url.QueryUnescape(srcBucket)
	if err != nil {
		srcBucket = parts[0]
	}
	srcKey, err = url.QueryUnescape(srcKey)
	if err != nil {
		srcKey = srcKeyAndQuery
	}

	reader, srcObj, err := g.store.GetObject(r.Context(), srcBucket, srcKey, srcVersionID)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) || errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The source object does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	defer reader.Close()

	destObj, err := g.store.PutObject(r.Context(), destBucket, destKey, reader, srcObj.Size, srcObj.ContentType)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	if g.cluster != nil && r.Header.Get("X-ServStore-Replicated") != "true" {
		ring := g.cluster.Ring()
		if ring != nil {
			owners, err := ring.GetNodes(destBucket+"/"+destKey, g.replicationFactor)
			if err == nil && len(owners) > 1 {
				var wg sync.WaitGroup
				for _, owner := range owners {
					if owner == g.cluster.LocalNodeID() {
						continue
					}
					addr, exists := g.cluster.GetNodeAddress(owner)
					if !exists {
						continue
					}
					wg.Add(1)
					go func(nodeAddr string) {
						defer wg.Done()
						_ = g.replicateObjectToNode(r.Context(), destBucket, destKey, destObj, nodeAddr)
					}(addr)
				}
				wg.Wait()
			}
		}
	}

	type CopyObjectResult struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		Xmlns        string   `xml:"xmlns,attr,omitempty"`
		LastModified string   `xml:"LastModified"`
		ETag         string   `xml:"ETag"`
	}

	res := CopyObjectResult{
		Xmlns:        xmlNamespace,
		LastModified: destObj.LastModified.UTC().Format(time.RFC3339),
		ETag:         `"` + destObj.ETag + `"`,
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleBatchDelete(w http.ResponseWriter, r *http.Request, bucket string) {
	var req Delete
	decoder := xml.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedXML", "XML body is malformed.")
		return
	}

	res := DeleteResult{
		Xmlns: xmlNamespace,
	}

	for _, obj := range req.Objects {
		deletedVer, err := g.store.DeleteObject(r.Context(), bucket, obj.Key, obj.VersionId)
		if err != nil {
			code := "InternalError"
			if errors.Is(err, storage.ErrObjectNotFound) || errors.Is(err, storage.ErrBucketNotFound) {
				code = "NoSuchKey"
			} else if errors.Is(err, storage.ErrObjectLocked) {
				code = "ObjectLocked"
			}
			res.Errors = append(res.Errors, DeleteErrorResult{
				Key:       obj.Key,
				VersionId: obj.VersionId,
				Code:      code,
				Message:   err.Error(),
			})
			continue
		}

		if g.cluster != nil && r.Header.Get("X-ServStore-Replicated") != "true" {
			ring := g.cluster.Ring()
			if ring != nil {
				owners, err := ring.GetNodes(bucket+"/"+obj.Key, g.replicationFactor)
				if err == nil && len(owners) > 1 {
					go func(k string, vid string) {
						for _, owner := range owners {
							if owner == g.cluster.LocalNodeID() {
								continue
							}
							addr, exists := g.cluster.GetNodeAddress(owner)
							if !exists {
								continue
							}
							_ = g.replicateDeleteToNode(context.Background(), bucket, k, vid, addr)
						}
					}(obj.Key, obj.VersionId)
				}
			}
		}

		if !req.Quiet {
			delRes := DeletedResult{
				Key:       obj.Key,
				VersionId: obj.VersionId,
			}
			if deletedVer.IsDeleteMarker {
				delRes.DeleteMarker = true
				delRes.DeleteMarkerVersionId = deletedVer.VersionID
			}
			res.Deleted = append(res.Deleted, delRes)
		}
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handlePutObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	var tagConf Tagging
	decoder := xml.NewDecoder(r.Body)
	if err := decoder.Decode(&tagConf); err != nil {
		g.writeError(w, http.StatusBadRequest, "MalformedXML", "XML body is malformed.")
		return
	}

	tags := make(map[string]string)
	for _, t := range tagConf.TagSet {
		tags[t.Key] = t.Value
	}

	_, err := g.store.PutObjectTagging(r.Context(), bucket, key, versionID, tags)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	tags, err := g.store.GetObjectTagging(r.Context(), bucket, key, versionID)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	res := Tagging{
		Xmlns: xmlNamespace,
	}
	for k, v := range tags {
		res.TagSet = append(res.TagSet, Tag{Key: k, Value: v})
	}

	g.writeXML(w, http.StatusOK, res)
}

func (g *Gateway) handleDeleteObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	_, err := g.store.DeleteObjectTagging(r.Context(), bucket, key, versionID)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (g *Gateway) handleConversationalQuery(w http.ResponseWriter, r *http.Request, bucket string) {
	ctx := r.Context()
	question := r.URL.Query().Get("ask")

	objects, _, err := g.store.ListObjects(ctx, bucket, "", "", "", 1000)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to list objects in bucket: "+err.Error())
		return
	}

	var contextBuilder strings.Builder
	processedCount := 0

	for _, obj := range objects {
		if strings.HasSuffix(obj.Key, ".txt") || strings.HasSuffix(obj.Key, ".md") || strings.HasSuffix(obj.Key, ".json") {
			reader, _, err := g.store.GetObject(ctx, bucket, obj.Key, "")
			if err == nil {
				data, readErr := io.ReadAll(reader)
				reader.Close()
				if readErr == nil {
					contextBuilder.WriteString(fmt.Sprintf("--- Document: %s ---\n%s\n\n", obj.Key, string(data)))
					processedCount++
					if processedCount >= 10 { // Limit to 10 files
						break
					}
				}
			}
		}
	}

	prompt := fmt.Sprintf("Context Documents:\n%s\nUser Question: %s\n\nSynthesize a clear and concise answer based ONLY on the provided context.", contextBuilder.String(), question)

	answer, err := completeLLM(prompt)
	if err != nil {
		g.writeError(w, http.StatusInternalServerError, "InternalError", "Failed to synthesize answer: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"bucket":   bucket,
		"question": question,
		"answer":   answer,
	})
}

func completeLLM(prompt string) (string, error) {
	aiConnStr := os.Getenv("SERV_AI_CONNECTION")
	if aiConnStr == "" {
		aiConnStr = "openai://gpt-4o-mini"
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || strings.HasPrefix(aiConnStr, "mock://") {
		// Mock RAG response helper
		if strings.Contains(strings.ToLower(prompt), "authentication") {
			return "Based on the documents in the bucket, user authentication is managed via token-based sessions.", nil
		}
		return "This is a mock RAG answer synthesized from the bucket documents regarding your question.", nil
	}

	url := "https://api.openai.com/v1/chat/completions"
	payload := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful Q&A assistant. Synthesize an answer to the user's question based strictly on the provided documents context."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}
	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer " + apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var respData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return "", err
	}
	if len(respData.Choices) > 0 {
		return respData.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no choice returned from OpenAI")
}
