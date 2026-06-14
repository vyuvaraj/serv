package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var assetsFS embed.FS

type WebConsole struct {
	gateway http.Handler
	fileServer http.Handler
}

func NewWebConsole(gateway http.Handler) *WebConsole {
	// Strip assets prefix
	subFS, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}

	return &WebConsole{
		gateway:    gateway,
		fileServer: http.FileServer(http.FS(subFS)),
	}
}

func (wc *WebConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	accept := r.Header.Get("Accept")

	// Check if this is a request for a web console asset
	isAsset := false
	if path == "/style.css" || path == "/app.js" || path == "/favicon.ico" {
		isAsset = true
	} else if path == "/" && strings.Contains(accept, "text/html") {
		// Serve index.html for root if browser requests HTML
		r.URL.Path = "/index.html"
		isAsset = true
	}

	if isAsset {
		wc.fileServer.ServeHTTP(w, r)
		return
	}

	// Otherwise, serve it through the S3 Gateway
	wc.gateway.ServeHTTP(w, r)
}
