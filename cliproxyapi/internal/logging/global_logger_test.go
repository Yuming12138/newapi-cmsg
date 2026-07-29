package logging

import (
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

func TestLogFormatterPrintsVersionField(t *testing.T) {
	entry := log.NewEntry(log.New())
	entry.Time = time.Date(2026, 6, 9, 11, 10, 2, 0, time.Local)
	entry.Level = log.InfoLevel
	entry.Message = "fetched latest antigravity version"
	entry.Data["version"] = "2.1.0"

	formatted, errFormat := (&LogFormatter{}).Format(entry)
	if errFormat != nil {
		t.Fatalf("Format() error = %v", errFormat)
	}

	line := string(formatted)
	if !strings.Contains(line, "version=2.1.0") {
		t.Fatalf("formatted line %q missing version field", line)
	}
}

func TestLogFormatterPrintsTransportShadowFields(t *testing.T) {
	entry := log.NewEntry(log.New())
	entry.Time = time.Date(2026, 7, 29, 9, 30, 0, 0, time.UTC)
	entry.Level = log.WarnLevel
	entry.Message = "transport recovery shadow: failure observed"
	entry.Data["shadow_mode"] = true
	entry.Data["host"] = "chatgpt.com"
	entry.Data["proxy_route"] = "http://mihomo:7890"
	entry.Data["selected_node"] = "unknown"
	entry.Data["selected_node_source"] = "controller_not_configured"
	entry.Data["connection_id"] = uint64(42)
	entry.Data["pool_generation"] = uint64(3)
	entry.Data["failure_phase"] = "response_body"
	entry.Data["failure_class"] = "h2_internal_error"
	entry.Data["actual_action"] = "drain_connection"
	entry.Data["shadow_action"] = "mark_node_suspect"
	entry.Data["payload_committed"] = false
	entry.Data["payload_boundary_known"] = true
	entry.Data["shadow_replay_eligible"] = true
	entry.Data["retry_attempt"] = 0
	entry.Data["retry_budget"] = 1
	entry.Data["retry_outcome"] = "succeeded"
	entry.Data["failure_class_total"] = uint64(2)
	entry.Data["shadow_action_total"] = uint64(2)
	entry.Data["shadow_node_switch_total"] = uint64(0)
	entry.Data["retry_outcome_total"] = uint64(9)
	entry.Data["unlisted_sensitive_field"] = "must-not-be-printed"

	formatted, errFormat := (&LogFormatter{}).Format(entry)
	if errFormat != nil {
		t.Fatalf("Format() error = %v", errFormat)
	}

	line := string(formatted)
	for _, want := range []string{
		"shadow_mode=true",
		"host=chatgpt.com",
		"proxy_route=http://mihomo:7890",
		"selected_node=unknown",
		"selected_node_source=controller_not_configured",
		"connection_id=42",
		"pool_generation=3",
		"failure_phase=response_body",
		"failure_class=h2_internal_error",
		"actual_action=drain_connection",
		"shadow_action=mark_node_suspect",
		"payload_committed=false",
		"payload_boundary_known=true",
		"shadow_replay_eligible=true",
		"retry_attempt=0",
		"retry_budget=1",
		"retry_outcome=succeeded",
		"failure_class_total=2",
		"shadow_action_total=2",
		"shadow_node_switch_total=0",
		"retry_outcome_total=9",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted line %q missing %s", line, want)
		}
	}
	if strings.Contains(line, "unlisted_sensitive_field") || strings.Contains(line, "must-not-be-printed") {
		t.Fatalf("formatted line %q contains an unlisted field", line)
	}
}

func TestLogFormatterPrintsPluginFields(t *testing.T) {
	entry := log.NewEntry(log.New())
	entry.Time = time.Date(2026, 6, 25, 20, 10, 0, 0, time.Local)
	entry.Level = log.InfoLevel
	entry.Message = "pluginhost: plugin loaded"
	entry.Data["plugin_id"] = "sample-provider"
	entry.Data["plugin_name"] = "Sample Provider"
	entry.Data["version"] = "0.2.0"
	entry.Data["active_version"] = "0.1.0"
	entry.Data["retired_version"] = "0.2.0"
	entry.Data["path"] = "plugins/windows/amd64/sample-provider-v0.2.0.dll"
	entry.Data["active_path"] = "plugins/windows/amd64/sample-provider-v0.1.0.dll"
	entry.Data["retired_path"] = "plugins/windows/amd64/sample-provider-v0.2.0.dll"

	formatted, errFormat := (&LogFormatter{}).Format(entry)
	if errFormat != nil {
		t.Fatalf("Format() error = %v", errFormat)
	}

	line := string(formatted)
	for _, want := range []string{
		"plugin_id=sample-provider",
		"plugin_name=Sample Provider",
		"version=0.2.0",
		"active_version=0.1.0",
		"retired_version=0.2.0",
		"path=plugins/windows/amd64/sample-provider-v0.2.0.dll",
		"active_path=plugins/windows/amd64/sample-provider-v0.1.0.dll",
		"retired_path=plugins/windows/amd64/sample-provider-v0.2.0.dll",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted line %q missing %s", line, want)
		}
	}
}

func TestLogFormatterOmitsGenericPathField(t *testing.T) {
	entry := log.NewEntry(log.New())
	entry.Time = time.Date(2026, 6, 25, 20, 20, 0, 0, time.Local)
	entry.Level = log.WarnLevel
	entry.Message = "failed to roll back token"
	entry.Data["path"] = "auths/private-token.json"
	entry.Data["active_path"] = "plugins/windows/amd64/sample-provider-v0.1.0.dll"
	entry.Data["retired_path"] = "plugins/windows/amd64/sample-provider-v0.2.0.dll"

	formatted, errFormat := (&LogFormatter{}).Format(entry)
	if errFormat != nil {
		t.Fatalf("Format() error = %v", errFormat)
	}

	line := string(formatted)
	for _, forbidden := range []string{"path=", "active_path=", "retired_path="} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("formatted line %q contains generic %s field", line, forbidden)
		}
	}
}
