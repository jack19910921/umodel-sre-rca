package inbound

import (
	"testing"
	"time"
)

func TestParseCloudMonitorOccurredEvent(t *testing.T) {
	event, err := ParseCloudMonitorEvent([]byte(`{
		"id":"event-1",
		"type":"ALERT",
		"subtype":"NORMAL_TRIGGER",
		"status":"OCCURRED",
		"severity":"WARNING",
		"workspace":"default-cms-test-cn-hangzhou",
		"ruleId":"rule-1",
		"alertEntityId":"entity-1",
		"time":"2026-07-22T15:14:40Z",
		"resource":{"entity":{"domain":"ecs","entity_type":"instance","entity_id":"i-demo"},"tags":{"instanceId":"i-demo"}},
		"data":{"currentValue":52.867},
		"labels":{"_cms_rule_id":"rule-1"},
		"annotations":{"current_value":"52.867"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Transition != AlertOccurred || event.Alert.ResourceID != "i-demo" || event.Alert.RuleID != "rule-1" {
		t.Fatalf("event=%+v", event)
	}
	if !event.Alert.EventAt.Equal(time.Date(2026, 7, 22, 15, 14, 40, 0, time.UTC)) {
		t.Fatalf("event_at=%s", event.Alert.EventAt)
	}
}

func TestParseCloudMonitorRecoveredEvent(t *testing.T) {
	event, err := ParseCloudMonitorEvent([]byte(`{
		"type":"ALERT",
		"subtype":"NORMAL_TRIGGER",
		"status":"RECOVERED",
		"workspace":"ws",
		"ruleId":"rule-1",
		"alertEntityId":"entity-1",
		"timestamp":1784733583874,
		"resource":{"entity":{"entity_id":"i-demo"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Transition != AlertRecovered || event.Alert.ResourceID != "i-demo" {
		t.Fatalf("event=%+v", event)
	}
}

func TestParseCloudMonitorEventAcceptsCloudMonitorOffsetWithoutColon(t *testing.T) {
	event, err := ParseCloudMonitorEvent([]byte(`{
		"type":"ALERT", "status":"OCCURRED", "workspace":"ws", "ruleId":"rule-1",
		"time":"2026-07-22T23:13:33+0800",
		"resource":{"entity":{"entity_id":"i-demo"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !event.Alert.EventAt.Equal(time.Date(2026, 7, 22, 15, 13, 33, 0, time.UTC)) {
		t.Fatalf("event_at=%s", event.Alert.EventAt)
	}
}

func TestParseCloudMonitorEventRejectsUnsupportedStatus(t *testing.T) {
	_, err := ParseCloudMonitorEvent([]byte(`{
		"type":"ALERT", "status":"UNKNOWN", "workspace":"ws", "ruleId":"rule-1",
		"resource":{"entity":{"entity_id":"i-demo"}}
	}`))
	if err == nil {
		t.Fatal("expected unsupported status error")
	}
}

func TestParseCloudMonitorEventIgnoresAdditionalCloudMonitorFields(t *testing.T) {
	_, err := ParseCloudMonitorEvent([]byte(`{
		"type":"ALERT", "status":"OCCURRED", "workspace":"ws", "ruleId":"rule-1",
		"resource":{"entity":{"entity_id":"i-demo"}},
		"time":"2026-07-22T15:14:40Z",
		"labels":{"_cms_rule_id":"rule-1"},
		"annotations":{"current_value":"52.867"},
		"data":{"currentValue":52.867}
	}`))
	if err != nil {
		t.Fatal(err)
	}
}
