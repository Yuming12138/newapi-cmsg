package common

import "testing"

func TestParseProxyURLStrict(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
		wantNil bool
	}{
		{name: "empty", raw: "", wantNil: true},
		{name: "http", raw: " HTTP://proxy.example:8080/ ", want: "http://proxy.example:8080"},
		{name: "socks default port", raw: "socks5://proxy.example", want: "socks5://proxy.example:1080"},
		{name: "unsupported scheme", raw: "ftp://proxy.example", wantErr: true},
		{name: "missing host", raw: "http://", wantErr: true},
		{name: "invalid port", raw: "http://proxy.example:0", wantErr: true},
		{name: "path", raw: "http://proxy.example/path", wantErr: true},
		{name: "query", raw: "http://proxy.example?x=1", wantErr: true},
		{name: "fragment", raw: "http://proxy.example#x", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsedURL, err := ParseProxyURLStrict(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseProxyURLStrict(%q) error = nil", test.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProxyURLStrict(%q) error = %v", test.raw, err)
			}
			if test.wantNil {
				if parsedURL != nil {
					t.Fatalf("ParseProxyURLStrict(%q) = %v, want nil", test.raw, parsedURL)
				}
				return
			}
			if got := parsedURL.String(); got != test.want {
				t.Fatalf("ParseProxyURLStrict(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestParseProxyURLRuntimeStripsLegacySuffix(t *testing.T) {
	parsedURL, stripped, err := ParseProxyURLRuntime("http://proxy.example:8080/legacy?x=1#fragment")
	if err != nil {
		t.Fatalf("ParseProxyURLRuntime() error = %v", err)
	}
	if !stripped {
		t.Fatal("ParseProxyURLRuntime() stripped = false, want true")
	}
	if got := parsedURL.String(); got != "http://proxy.example:8080" {
		t.Fatalf("ParseProxyURLRuntime() = %q, want canonical proxy URL", got)
	}
}
