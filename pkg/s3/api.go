package s3

import (
	"bytes"
	"context"
	"crypto/md5"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/reedsolomon"
	"servstore/pkg/auth"
	"servstore/pkg/cluster"
	"servstore/pkg/metrics"
	"servstore/pkg/otel"
	"servstore/pkg/storage"
)

type Gateway struct {
	store             storage.StorageEngine
	auth              *auth.AuthProvider
	raftNode          *cluster.RaftNode
	cluster           *cluster.MembershipManager
	replicationFactor int
	erasureEnabled    bool
	dataShards        int
	parityShards      int
	crrMgr            *cluster.CRRManager
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
	return &Gateway{
		store:             store,
		auth:              auth,
		raftNode:          raftNode,
		cluster:           clusterMgr,
		replicationFactor: replicationFactor,
		erasureEnabled:    erasureEnabled,
		dataShards:        dataShards,
		parityShards:      parityShards,
	}
}

func (g *Gateway) WithCRR(crrMgr *cluster.CRRManager) *Gateway {
	g.crrMgr = crrMgr
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

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Intercept Prometheus metrics endpoint
	if r.URL.Path == "/metrics" && r.Method == http.MethodGet {
		metrics.Handler().ServeHTTP(w, r)
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
		g.writeError(w, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	}

	// Verify RBAC Authorization
	if !g.checkAuthorization(r) {
		g.writeError(w, http.StatusForbidden, "AccessDenied", "Access Denied by RBAC Policy")
		return
	}

	// Parse bucket and key
	bucket, key := parsePath(r.URL.Path)

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
			} else {
				g.handleListObjects(w, r, bucket)
			}
		case http.MethodPut:
			if r.URL.Query().Has("versioning") {
				g.handlePutBucketVersioning(w, r, bucket)
			} else if r.URL.Query().Has("lifecycle") {
				g.handlePutBucketLifecycle(w, r, bucket)
			} else {
				g.handleCreateBucket(w, r, bucket)
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
		g.handleGetObject(w, r, bucket, key)
	case http.MethodPut:
		if query.Has("uploadId") && query.Has("partNumber") {
			g.handleUploadPart(w, r, bucket, key)
		} else if query.Has("lock") {
			g.handleLockObject(w, r, bucket, key)
		} else {
			g.handlePutObject(w, r, bucket, key)
		}
	case http.MethodPost:
		if query.Has("uploads") {
			g.handleInitiateMultipart(w, r, bucket, key)
		} else if query.Has("uploadId") {
			g.handleCompleteMultipart(w, r, bucket, key)
		} else {
			g.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed on object level")
		}
	case http.MethodDelete:
		if query.Has("uploadId") {
			g.handleAbortMultipart(w, r, bucket, key)
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

func (g *Gateway) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	query := r.URL.Query()
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

	objects, commonPrefixes, err := g.store.ListObjects(r.Context(), bucket, prefix, delimiter, marker, maxKeys)
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
	size := r.ContentLength
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ctx := r.Context()
	if headerVer := r.Header.Get("X-ServStore-Version-Id"); headerVer != "" {
		ctx = context.WithValue(ctx, storage.VersionIDContextKey, headerVer)
	}

	obj, err := g.store.PutObject(ctx, bucket, key, r.Body, size, contentType)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

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

	reader, obj, err := g.store.GetObject(r.Context(), bucket, key, versionID)
	
	// If the reader is an integrityCheckingReader, we can read/verify it fully before writing headers,
	// or we can read it to a buffer so we can catch integrity errors before sending HTTP headers.
	// Since we need to support failover on integrity corruption, we must verify the integrity before sending headers.
	var buf []byte
	if err == nil {
		buf, err = io.ReadAll(reader)
		reader.Close()
	}

	if err != nil {
		isIntegrityErr := err != nil && strings.Contains(err.Error(), "integrity corruption detected")
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
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	if obj.Checksum != "" {
		w.Header().Set("x-amz-meta-blake3", obj.Checksum)
	}

	_, _ = w.Write(buf)
}

func (g *Gateway) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	obj, err := g.store.HeadObject(r.Context(), bucket, key, versionID)
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
		default:
			return false
		}
	} else {
		resource = "arn:aws:s3:::" + bucket + "/" + key
		switch r.Method {
		case http.MethodGet:
			action = "s3:GetObject"
		case http.MethodPut:
			action = "s3:PutObject"
		case http.MethodDelete:
			action = "s3:DeleteObject"
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

func (g *Gateway) proposeOrExecute(w http.ResponseWriter, r *http.Request, op string, bucketName, keyName string, value []byte, fallback func() error) error {
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

