package dto

import "testing"

func TestAdvancedCustomConfigMapReasoningEffort(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *AdvancedCustomConfig
		request string
		want    string
	}{
		{
			name:    "nil config leaves the request untouched",
			cfg:     nil,
			request: "xhigh",
			want:    "xhigh",
		},
		{
			name:    "no mapping leaves the request untouched",
			cfg:     &AdvancedCustomConfig{},
			request: "xhigh",
			want:    "xhigh",
		},
		{
			name:    "configured level is rewritten to max",
			cfg:     &AdvancedCustomConfig{ReasoningEffortMap: map[string]string{"xhigh": "max"}},
			request: "xhigh",
			want:    "max",
		},
		{
			name:    "mapping is case insensitive on the key",
			cfg:     &AdvancedCustomConfig{ReasoningEffortMap: map[string]string{"xhigh": "max"}},
			request: "XHigh",
			want:    "max",
		},
		{
			name:    "unlisted level is preserved",
			cfg:     &AdvancedCustomConfig{ReasoningEffortMap: map[string]string{"xhigh": "max"}},
			request: "low",
			want:    "low",
		},
		{
			name:    "blank target falls back to the requested level",
			cfg:     &AdvancedCustomConfig{ReasoningEffortMap: map[string]string{"xhigh": "  "}},
			request: "xhigh",
			want:    "xhigh",
		},
		{
			name:    "empty request is left empty",
			cfg:     &AdvancedCustomConfig{ReasoningEffortMap: map[string]string{"xhigh": "max"}},
			request: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.MapReasoningEffort(tc.request); got != tc.want {
				t.Fatalf("MapReasoningEffort(%q) = %q, want %q", tc.request, got, tc.want)
			}
		})
	}
}
