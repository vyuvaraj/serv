package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"servgate/pkg/otel"
	"servgate/pkg/wasm"
)

type Route struct {
	Prefix     string `json:"prefix"`
	Target     string `json:"target"`
	Middleware string `json:"middleware,omitempty"`
}

type GatewayHandler struct {
	routes    []Route
	wasm      *wasm.MiddlewareManager
	authToken string
}

func NewGatewayHandler(routes []Route, wasm *wasm.MiddlewareManager, authToken string) *GatewayHandler {
	return &GatewayHandler{
		routes:    routes,
		wasm:      wasm,
		authToken: authToken,
	}
}

func (h *GatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Authentication
	if h.authToken != "" {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != h.authToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Distributed Tracing: Extract or start trace context span
	traceparent := r.Header.Get("traceparent")
	span := otel.StartSpan(fmt.Sprintf("%s %s", r.Method, r.URL.Path), traceparent)
	
	// Inject trace context headers
	if span != nil {
		traceparent = fmt.Sprintf("00-%s-%s-01", span.TraceID, span.SpanID)
		r.Header.Set("traceparent", traceparent)
	}

	// Route Matching
	var matchedRoute *Route
	for _, route := range h.routes {
		if strings.HasPrefix(r.URL.Path, route.Prefix) {
			matchedRoute = &route
			break
		}
	}

	if matchedRoute == nil {
		otel.EndSpan(span, fmt.Errorf("Route not found"), map[string]interface{}{})
		http.Error(w, "Bad gateway: route match not found", http.StatusBadGateway)
		return
	}

	// WASM Request Middleware execution if registered
	if matchedRoute.Middleware != "" {
		// Read body to pass as input
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body.Close()

		wasmSpan := otel.StartSpan(fmt.Sprintf("WASM Middleware %s", matchedRoute.Middleware), traceparent)
		outputBytes, err := h.wasm.Run(r.Context(), matchedRoute.Middleware, bodyBytes)
		otel.EndSpan(wasmSpan, err, map[string]interface{}{})

		if err != nil {
			otel.EndSpan(span, err, map[string]interface{}{})
			http.Error(w, "Internal Server Error: WASM Middleware execution failed", http.StatusInternalServerError)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(outputBytes))
		r.ContentLength = int64(len(outputBytes))
	}

	// Reverse Proxy Forwarding
	targetURL, err := url.Parse(matchedRoute.Target)
	if err != nil {
		otel.EndSpan(span, err, map[string]interface{}{})
		http.Error(w, "Bad Gateway Target", http.StatusBadGateway)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	
	// Adjust path matching prefixes
	r.URL.Host = targetURL.Host
	r.URL.Scheme = targetURL.Scheme
	r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
	r.Host = targetURL.Host

	// Custom Director rewrite to strip routing prefix
	r.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, matchedRoute.Prefix), "/")

	// Execute proxy forwarding
	proxy.ServeHTTP(w, r)
	otel.EndSpan(span, nil, map[string]interface{}{
		"http.route": matchedRoute.Prefix,
		"proxy.target": matchedRoute.Target,
	})
}
