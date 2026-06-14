package s3

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/metrics"
	"servstore/pkg/otel"
	"servstore/pkg/storage"
)

type Gateway struct {
	store storage.StorageEngine
	auth  *auth.AuthProvider
}

func NewGateway(store storage.StorageEngine, auth *auth.AuthProvider) *Gateway {
	return &Gateway{
		store: store,
		auth:  auth,
	}
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
	err := g.store.CreateBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketExists) {
			g.writeError(w, http.StatusConflict, "BucketAlreadyExists", "The requested bucket name is not available.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	err := g.store.DeleteBucket(r.Context(), bucket)
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

	err := g.store.SetBucketVersioning(r.Context(), bucket, config.Status)
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

	obj, err := g.store.PutObject(r.Context(), bucket, key, r.Body, size, contentType)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			g.writeError(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		g.writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	reader, obj, err := g.store.GetObject(r.Context(), bucket, key, versionID)
	if err != nil {
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
	defer reader.Close()

	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}

	_, _ = io.Copy(w, reader)
}

func (g *Gateway) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	obj, err := g.store.HeadObject(r.Context(), bucket, key, versionID)
	if err != nil {
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

	if obj.IsDeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
	}
	if obj.VersionID != "" && obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	w.WriteHeader(http.StatusNoContent)
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

	if err := g.store.SetBucketLifecycle(r.Context(), bucket, rules); err != nil {
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
	if err := g.store.DeleteBucketLifecycle(r.Context(), bucket); err != nil {
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
