package controller

import "testing"

func TestIsMoonshotBalanceBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "canonical", raw: "https://api.moonshot.cn", want: true},
		{name: "with path", raw: "https://api.moonshot.cn/v1", want: true},
		{name: "case insensitive", raw: "HTTPS://API.MOONSHOT.CN/", want: true},
		{name: "lookalike host", raw: "https://api.moonshot.cn.example.com", want: false},
		{name: "different host", raw: "https://moonshot.cn", want: false},
		{name: "empty", raw: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMoonshotBalanceBaseURL(tt.raw); got != tt.want {
				t.Fatalf("isMoonshotBalanceBaseURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
