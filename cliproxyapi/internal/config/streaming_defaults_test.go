package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_StreamingBootstrapRetriesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.Streaming.BootstrapRetries != DefaultStreamingBootstrapRetries {
		t.Fatalf("bootstrap retries = %d, want %d", cfg.Streaming.BootstrapRetries, DefaultStreamingBootstrapRetries)
	}
}

func TestLoadConfigOptional_StreamingBootstrapRetriesExplicitZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("streaming:\n  bootstrap-retries: 0\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.Streaming.BootstrapRetries != 0 {
		t.Fatalf("bootstrap retries = %d, want 0", cfg.Streaming.BootstrapRetries)
	}
}

func TestParseConfigBytes_StreamingBootstrapRetriesDefault(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte("port: 8317\n"))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}
	if cfg.Streaming.BootstrapRetries != DefaultStreamingBootstrapRetries {
		t.Fatalf("bootstrap retries = %d, want %d", cfg.Streaming.BootstrapRetries, DefaultStreamingBootstrapRetries)
	}
}
