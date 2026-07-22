package httpapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/inbound"
)

const maxCallbackBytes = 256 * 1024

type incidentRepository interface {
	CreateOrGetIncident(context.Context, domain.Incident) (domain.Incident, bool, error)
	Enqueue(context.Context, string, time.Time) error
	MarkRecovered(context.Context, string, time.Time) error
}

func NewGateway(callbackToken, captureDir string, repositories ...incidentRepository) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	if len(repositories) == 1 && repositories[0] != nil {
		registerCloudMonitorIngress(mux, callbackToken, repositories[0])
	}
	if captureDir == "" {
		return mux
	}
	capture := inbound.NewCapture(captureDir)
	mux.HandleFunc("POST /v1/inbound/cloudmonitor/capture", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, callbackToken) {
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

func registerCloudMonitorIngress(mux *http.ServeMux, callbackToken string, repo incidentRepository) {
	mux.HandleFunc("POST /v1/inbound/cloudmonitor", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, callbackToken) {
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
		event, err := inbound.ParseCloudMonitorEvent(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid CloudMonitor callback"})
			return
		}
		if event.Transition == inbound.AlertRecovered {
			if err := repo.MarkRecovered(r.Context(), domain.NewIncident(event.Alert.Workspace, event.Alert.RuleID, event.Alert.ResourceID, event.Alert.EventAt).Key, event.Alert.EventAt); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "state": "recovery_ignored"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist recovery"})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "state": "recovered"})
			return
		}
		incident, created, err := repo.CreateOrGetIncident(r.Context(), domain.NewIncident(event.Alert.Workspace, event.Alert.RuleID, event.Alert.ResourceID, event.Alert.EventAt))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist incident"})
			return
		}
		if created {
			if err := repo.Enqueue(r.Context(), incident.ID, event.Alert.EventAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "queue incident"})
				return
			}
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "incident_id": incident.ID, "duplicate": !created})
	})
}

func authorized(r *http.Request, callbackToken string) bool {
	return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(callbackToken)) == 1
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
