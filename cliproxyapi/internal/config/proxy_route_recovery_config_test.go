package config

import "testing"

func TestParseConfigBytesProxyRouteRecovery(t *testing.T) {
	t.Parallel()

	cfg, err := ParseConfigBytes([]byte(`
proxy-route-recovery:
  enabled: true
  controller-url: http://mihomo:9090
  controller-secret-file: /app/secrets/mihomo-controller
  group: OpenAI稳定
  hosts: [chatgpt.com]
  h2-error-window: 30s
  h2-error-threshold: 2
  node-cooldown: 15m
  repeated-failure-cooldown: 30m
  route-hold: 10m
  max-replays: 1
`))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}
	recovery := cfg.ProxyRouteRecovery
	if !recovery.Enabled || recovery.ControllerURL != "http://mihomo:9090" {
		t.Fatalf("proxy route recovery base config = %#v", recovery)
	}
	if recovery.Group != "OpenAI稳定" || len(recovery.Hosts) != 1 || recovery.Hosts[0] != "chatgpt.com" {
		t.Fatalf("proxy route recovery target = %#v", recovery)
	}
	if recovery.H2ErrorWindow != "30s" || recovery.H2ErrorThreshold != 2 || recovery.MaxReplays != 1 {
		t.Fatalf("proxy route recovery policy = %#v", recovery)
	}
}
