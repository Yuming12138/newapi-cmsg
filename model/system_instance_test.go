package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestSystemInstanceToResponseStatusAndInfo(t *testing.T) {
	instance := &SystemInstance{
		NodeName:   "node-a",
		Info:       `{"runtime":{"version":"test-version"}}`,
		StartedAt:  100,
		LastSeenAt: 1000,
	}

	response := instance.ToResponse(1000 + SystemInstanceStaleAfterSeconds)
	if response.Status != SystemInstanceStatusOnline {
		t.Fatalf("expected online status, got %q", response.Status)
	}
	if response.NodeName != "node-a" {
		t.Fatalf("expected node name node-a, got %q", response.NodeName)
	}
	if response.StaleAfterSeconds != SystemInstanceStaleAfterSeconds {
		t.Fatalf("unexpected stale threshold: %d", response.StaleAfterSeconds)
	}
	info, ok := response.Info.(map[string]any)
	if !ok {
		t.Fatalf("expected decoded info map, got %T", response.Info)
	}
	runtimeInfo, ok := info["runtime"].(map[string]any)
	if !ok || runtimeInfo["version"] != "test-version" {
		t.Fatalf("unexpected runtime info: %#v", info["runtime"])
	}

	response = instance.ToResponse(1001 + SystemInstanceStaleAfterSeconds)
	if response.Status != SystemInstanceStatusStale {
		t.Fatalf("expected stale status, got %q", response.Status)
	}
}

func TestSystemInstanceInfoMarshalRoundTrip(t *testing.T) {
	infoText, err := marshalSystemInstanceInfo(map[string]any{
		"node": map[string]any{"name": "node-b"},
	})
	if err != nil {
		t.Fatalf("marshalSystemInstanceInfo returned error: %v", err)
	}
	var decoded map[string]any
	if err := common.UnmarshalJsonStr(infoText, &decoded); err != nil {
		t.Fatalf("marshaled info is not valid JSON: %v", err)
	}
	node, ok := decoded["node"].(map[string]any)
	if !ok || node["name"] != "node-b" {
		t.Fatalf("unexpected decoded info: %#v", decoded)
	}
}
