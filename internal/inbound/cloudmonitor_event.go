package inbound

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

const (
	AlertOccurred  = "OCCURRED"
	AlertRecovered = "RECOVERED"
)

// CloudMonitorEvent is the minimum, stable subset of CloudMonitor 2.0's
// direct custom-webhook payload needed to create or resolve an RCA incident.
// Unknown fields are intentionally ignored and never persisted by the ingress.
type CloudMonitorEvent struct {
	Alert      domain.Alert
	Transition string
}

type cloudMonitorPayload struct {
	Type          string `json:"type"`
	Status        string `json:"status"`
	Workspace     string `json:"workspace"`
	RuleID        string `json:"ruleId"`
	AlertEntityID string `json:"alertEntityId"`
	Time          string `json:"time"`
	Timestamp     int64  `json:"timestamp"`
	Resource      struct {
		Entity struct {
			EntityID string `json:"entity_id"`
		} `json:"entity"`
	} `json:"resource"`
}

func ParseCloudMonitorEvent(raw []byte) (CloudMonitorEvent, error) {
	var payload cloudMonitorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&payload); err != nil {
		return CloudMonitorEvent{}, fmt.Errorf("decode CloudMonitor callback: %w", err)
	}
	if strings.ToUpper(payload.Type) != "ALERT" {
		return CloudMonitorEvent{}, fmt.Errorf("unsupported CloudMonitor event type %q", payload.Type)
	}
	transition := strings.ToUpper(payload.Status)
	if transition != AlertOccurred && transition != AlertRecovered {
		return CloudMonitorEvent{}, fmt.Errorf("unsupported CloudMonitor alert status %q", payload.Status)
	}
	resourceID := payload.Resource.Entity.EntityID
	if resourceID == "" {
		resourceID = payload.AlertEntityID
	}
	if payload.Workspace == "" || payload.RuleID == "" || resourceID == "" {
		return CloudMonitorEvent{}, fmt.Errorf("CloudMonitor callback requires workspace, ruleId, and resource entity id")
	}
	eventAt, err := cloudMonitorEventTime(payload.Time, payload.Timestamp)
	if err != nil {
		return CloudMonitorEvent{}, err
	}
	return CloudMonitorEvent{
		Transition: transition,
		Alert: domain.Alert{
			Workspace:  payload.Workspace,
			RuleID:     payload.RuleID,
			ResourceID: resourceID,
			State:      transition,
			EventAt:    eventAt,
		},
	}, nil
}

func cloudMonitorEventTime(value string, timestamp int64) (time.Time, error) {
	if value != "" {
		for _, layout := range []string{
			time.RFC3339Nano,
			"2006-01-02T15:04:05-0700",
		} {
			parsed, err := time.Parse(layout, value)
			if err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("parse CloudMonitor event time %q", value)
	}
	if timestamp > 0 {
		return time.UnixMilli(timestamp).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("CloudMonitor callback requires time or timestamp")
}
