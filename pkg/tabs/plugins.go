package tabs

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

type ConsolePlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	WASMUrl     string `json:"wasmUrl"`
}

var (
	ConsolePlugins = []ConsolePlugin{
		{ID: "db-inspector", Name: "SQL DB Inspector", Description: "Live PostgreSQL and MySQL query visualizer panel", WASMUrl: "/api/plugins/serve?id=db-inspector"},
	}
	ConsolePluginsMu sync.RWMutex
	PluginBinaries   = make(map[string][]byte)
	PluginBinariesMu sync.Mutex
)

func HandleGetPlugins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ConsolePluginsMu.RLock()
	defer ConsolePluginsMu.RUnlock()
	json.NewEncoder(w).Encode(ConsolePlugins)
}

func HandleRegisterPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		WriteJSONError(w, r, "Failed to parse multipart form: "+err.Error(), "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	id := r.FormValue("id")
	name := r.FormValue("name")
	desc := r.FormValue("description")

	file, _, err := r.FormFile("plugin")
	if err != nil {
		WriteJSONError(w, r, "Plugin WASM file is required", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}
	defer file.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		WriteJSONError(w, r, "Failed to read uploaded plugin: "+err.Error(), "ERR_INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}

	PluginBinariesMu.Lock()
	PluginBinaries[id] = buf.Bytes()
	PluginBinariesMu.Unlock()

	ConsolePluginsMu.Lock()
	exists := false
	for i, p := range ConsolePlugins {
		if p.ID == id {
			ConsolePlugins[i] = ConsolePlugin{ID: id, Name: name, Description: desc, WASMUrl: "/api/plugins/serve?id=" + id}
			exists = true
			break
		}
	}
	if !exists {
		ConsolePlugins = append(ConsolePlugins, ConsolePlugin{ID: id, Name: name, Description: desc, WASMUrl: "/api/plugins/serve?id=" + id})
	}
	ConsolePluginsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Plugin registered successfully",
		"id":      id,
	})
}

func HandleServePlugin(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	PluginBinariesMu.Lock()
	binary, ok := PluginBinaries[id]
	PluginBinariesMu.Unlock()

	if !ok {
		if id == "db-inspector" {
			binary = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
		} else {
			http.Error(w, "Plugin not found", http.StatusNotFound)
			return
		}
	}

	w.Header().Set("Content-Type", "application/wasm")
	w.WriteHeader(http.StatusOK)
	w.Write(binary)
}
