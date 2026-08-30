package home

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestHeartbeatOKWithinRequiresRecentSuccessfulHeartbeat(t *testing.T) {
	client := New(config.HomeConfig{Enabled: true})

	if client.HeartbeatOKWithin(DefaultHeartbeatGrace) {
		t.Fatal("HeartbeatOKWithin() = true before any successful heartbeat")
	}

	client.lastHeartbeatAt.Store(time.Now().Add(-5 * time.Second).UnixNano())
	if !client.HeartbeatOKWithin(10 * time.Second) {
		t.Fatal("HeartbeatOKWithin() = false inside the grace window")
	}

	client.lastHeartbeatAt.Store(time.Now().Add(-11 * time.Second).UnixNano())
	if client.HeartbeatOKWithin(10 * time.Second) {
		t.Fatal("HeartbeatOKWithin() = true after the grace window")
	}
}

func TestHeartbeatOKWithinRequiresEnabledHome(t *testing.T) {
	client := New(config.HomeConfig{Enabled: false})
	client.lastHeartbeatAt.Store(time.Now().UnixNano())

	if client.HeartbeatOKWithin(DefaultHeartbeatGrace) {
		t.Fatal("HeartbeatOKWithin() = true for a disabled Home client")
	}
}
