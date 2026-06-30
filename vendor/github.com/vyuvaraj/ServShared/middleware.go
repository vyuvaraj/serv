package ServShared

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ContextKey is the type for context keys to avoid collisions.
type ContextKey string

const (
	// ClaimsContextKey is the context key for authenticated claims.
	ClaimsContextKey ContextKey = "servverse-claims"
)

// GetClaims extracts claims from request context (set by AuthMiddleware).
func GetClaims(r *http.Request) *Claims {
	if c, ok := r.Context().Value(ClaimsContextKey).(*Claims); ok {
		return c
	}
	return nil
}

// AuthMiddleware returns an HTTP middleware that enforces JWT auth.
//
// Behavior:
//   - If SERV_JWT_SECRET is empty: all requests pass through (dev mode)
//   - If SERV_JWT_SECRET is set: requires valid Bearer JWT on all routes
//   - /healthz and /readyz are always allowed without auth
func AuthMiddleware(next http.Handler) http.Handler {
	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		return next // dev mode — no auth enforced
	}

	validator := NewAuthValidator(secret, "", "")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow health probes without auth
		path := r.URL.Path
		if path == "/healthz" || path == "/readyz" || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token
		token, err := ExtractTokenFromHeader(r.Header.Get("Authorization"))
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "Unauthorized: "+err.Error(), "ERR_MISSING_AUTH")
			return
		}

		// Validate
		claims, err := validator.ValidateToken(token)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "Unauthorized: "+err.Error(), "ERR_INVALID_TOKEN")
			return
		}

		// Inject claims into context
		ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GenerateServiceToken creates a long-lived JWT for inter-service communication.
// The token identifies the calling service and has the "service" role.
func GenerateServiceToken(secret string, serviceName string) (string, error) {
	if secret == "" {
		return "", nil // dev mode — no token needed
	}

	claims := Claims{
		Username: serviceName,
		Roles:    []string{"service"},
		Scopes:   []string{"*"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "servverse",
			Subject:   serviceName,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GenerateUserToken creates a JWT for a user with given roles.
func GenerateUserToken(secret string, username string, roles []string, ttl time.Duration) (string, error) {
	claims := Claims{
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "servverse",
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// HasRole checks if the authenticated claims include a specific role.
func HasRole(r *http.Request, role string) bool {
	claims := GetClaims(r)
	if claims == nil {
		return false
	}
	for _, r := range claims.Roles {
		if r == role || r == "admin" {
			return true
		}
	}
	return false
}

func writeAuthError(w http.ResponseWriter, status int, msg, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error": msg,
		"code":  code,
	})
}

// responseWriterWrapper wraps http.ResponseWriter to capture status code
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterWrapper) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// TraceMiddleware intercepts requests to create OTel spans and propagates trace headers
func TraceMiddleware(serviceName string, next http.Handler) http.Handler {
	InitTrace(serviceName)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent := r.Header.Get("traceparent")
		if traceparent == "" {
			traceparent = r.Header.Get("X-Request-ID")
		}

		span := StartSpan(fmt.Sprintf("%s %s", r.Method, r.URL.Path), traceparent)
		if span != nil {
			span.Kind = 2 // Server span
			tpVal := fmt.Sprintf("00-%s-%s-01", span.TraceID, span.SpanID)
			r.Header.Set("traceparent", tpVal)
		}

		wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		defer func() {
			if span != nil {
				attrs := map[string]interface{}{
					"http.method":      r.Method,
					"http.status_code": wrapper.statusCode,
					"http.url":         r.URL.String(),
				}
				var err error
				if wrapper.statusCode >= 400 {
					err = fmt.Errorf("HTTP error status %d", wrapper.statusCode)
				}
				EndSpan(span, err, attrs)
			}
		}()

		next.ServeHTTP(wrapper, r)
	})
}

// LogJSON logs a message in structured JSON format, extracting trace ID from request context if available
func LogJSON(r *http.Request, level, msg string) {
	entry := map[string]interface{}{
		"level": level,
		"ts":    time.Now().Format(time.RFC3339),
		"msg":   msg,
	}
	if r != nil {
		entry["method"] = r.Method
		entry["path"] = r.URL.Path
		tp := r.Header.Get("traceparent")
		if tp != "" {
			parts := strings.Split(tp, "-")
			if len(parts) >= 3 {
				entry["trace_id"] = parts[1]
			}
		}
	}
	data, _ := json.Marshal(entry)
	fmt.Println(string(data))
}
