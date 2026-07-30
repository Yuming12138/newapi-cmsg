package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestShouldStartExampleAPIKeyWarningServer(t *testing.T) {
	cfgWithExampleKey := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{"real-key", " your-api-key-1 "},
		},
	}
	cfgWithRealKey := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: []string{"real-key"},
		},
	}

	tests := []struct {
		name               string
		cfg                *config.Config
		commandMode        bool
		tuiMode            bool
		standalone         bool
		cloudConfigMissing bool
		homeMode           bool
		want               bool
	}{
		{
			name: "normal server with example key",
			cfg:  cfgWithExampleKey,
			want: true,
		},
		{
			name:       "standalone tui with example key",
			cfg:        cfgWithExampleKey,
			tuiMode:    true,
			standalone: true,
			want:       true,
		},
		{
			name:        "pure tui client is not blocked",
			cfg:         cfgWithExampleKey,
			tuiMode:     true,
			standalone:  false,
			commandMode: false,
			want:        false,
		},
		{
			name:        "one-shot command is not blocked",
			cfg:         cfgWithExampleKey,
			commandMode: true,
			want:        false,
		},
		{
			name:     "home mode is not blocked",
			cfg:      cfgWithExampleKey,
			homeMode: true,
			want:     false,
		},
		{
			name:               "cloud standby without config is not blocked",
			cfg:                cfgWithExampleKey,
			cloudConfigMissing: true,
			want:               false,
		},
		{
			name: "normal server with real key",
			cfg:  cfgWithRealKey,
			want: false,
		},
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldStartExampleAPIKeyWarningServer(tt.cfg, tt.commandMode, tt.tuiMode, tt.standalone, tt.cloudConfigMissing, tt.homeMode)
			if got != tt.want {
				t.Fatalf("shouldStartExampleAPIKeyWarningServer() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestMergeHomeRuntimeConfigPreservesNodeLocalSettings(t *testing.T) {
	localManagement := config.RemoteManagement{
		AllowRemote:            true,
		SecretKey:              "local-management-secret",
		DisableAutoUpdatePanel: true,
	}
	remoteCfg := &config.Config{
		Host:             "home.example.internal",
		Port:             9443,
		RemoteManagement: config.RemoteManagement{SecretKey: "home-management-secret"},
	}
	remoteCfg.ProxyURL = "http://mihomo:7890"
	localCfg := &config.Config{
		Host:             "127.0.0.1",
		Port:             8317,
		RemoteManagement: localManagement,
	}
	homeCfg := config.HomeConfig{Enabled: true}

	got := mergeHomeRuntimeConfig(remoteCfg, localCfg, homeCfg)

	if got.Host != localCfg.Host || got.Port != localCfg.Port {
		t.Fatalf("node bind settings = %s:%d, want local settings", got.Host, got.Port)
	}
	if got.RemoteManagement != localManagement {
		t.Fatal("expected local remote-management configuration to be preserved")
	}
	if got.ProxyURL != remoteCfg.ProxyURL {
		t.Fatal("expected shared Home proxy configuration to be preserved")
	}
	if !got.Home.Enabled {
		t.Fatal("expected Home configuration to be enabled")
	}
	if !got.UsageStatisticsEnabled {
		t.Fatal("expected Home runtime usage statistics to be enabled")
	}
}

func TestMergeHomeRuntimeConfigUsesSafeDefaultPort(t *testing.T) {
	got := mergeHomeRuntimeConfig(&config.Config{}, &config.Config{}, config.HomeConfig{Enabled: true})
	if got.Port != 8317 {
		t.Fatalf("port = %d, want 8317", got.Port)
	}
}
