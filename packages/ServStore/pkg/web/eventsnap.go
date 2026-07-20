package web

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

func (wc *WebConsole) handleEventSnapshots(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	suffix := strings.TrimPrefix(path, "/api/v1/events/snapshots/")
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 {
		WriteJSONError(w, r, "Invalid path", "ERR_INVALID_PATH", http.StatusBadRequest)
		return
	}
	stream := parts[0]
	id := parts[1]

	bucketName := "events-snapshots"
	_ = wc.store.CreateBucket(r.Context(), bucketName)

	key := stream + "/" + id

	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			WriteJSONError(w, r, "Bad Request", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}

		_, err = wc.store.PutObject(r.Context(), bucketName, key, bytes.NewReader(body), int64(len(body)), "application/json")
		if err != nil {
			WriteJSONError(w, r, err.Error(), "ERR_STORE_PUT_FAILED", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	case http.MethodGet:
		reader, _, err := wc.store.GetObject(r.Context(), bucketName, key, "")
		if err != nil {
			WriteJSONError(w, r, "Snapshot not found: "+err.Error(), "ERR_NOT_FOUND", http.StatusNotFound)
			return
		}
		defer reader.Close()

		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, reader)

	default:
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
	}
}
