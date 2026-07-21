package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/jack/umodel-sre-rca/internal/inbound"
)

const maxCallbackBytes = 256 * 1024

func NewGateway(callbackToken, captureDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	if captureDir == "" {
		return mux
	}
	capture := inbound.NewCapture(captureDir)
	mux.HandleFunc("POST /v1/inbound/cloudmonitor/capture", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(callbackToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]bool{"accepted": false})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCallbackBytes)
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read callback body"})
			return
		}
		fixtureID, err := capture.Save(r.Context(), raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "fixture_id": fixtureID})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
