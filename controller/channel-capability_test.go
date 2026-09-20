package controller

import "testing"

func TestCapabilityAnswerMatches(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"bare svg", `<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="10"/></svg>`, true},
		{"svg in markdown fence", "```svg\n<svg viewBox=\"0 0 100 100\"></svg>\n```", true},
		{"svg wrapped in explanation", "Here is the drawing:\n<svg viewBox=\"0 0 100 100\"></svg>", true},
		{"missing closing tag", `<svg viewBox="0 0 100 100">`, false},
		{"no svg", "The model returned an explanation but no drawing.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capabilityAnswerMatches(tt.text); got != tt.want {
				t.Fatalf("capabilityAnswerMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractCapabilitySVG(t *testing.T) {
	text := "```html\n<html><body><svg viewBox=\"0 0 20 20\"><circle r=\"5\"/></svg></body></html>\n```"
	got := extractCapabilitySVG(text)
	if got != `<svg viewBox="0 0 20 20"><circle r="5"/></svg>` {
		t.Fatalf("extractCapabilitySVG() = %q", got)
	}
}

func TestCapabilityFailureReason(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want string
	}{
		{"empty", "", "empty_response"},
		{"missing", "plain text", "missing_svg"},
		{"incomplete", "<svg>", "incomplete_svg"},
		{"complete but too large", "<svg>" + string(make([]byte, maxCapabilitySVGRunes+1)) + "</svg>", "invalid_svg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := capabilityFailureReason(false, tt.text); got != tt.want {
				t.Fatalf("capabilityFailureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapabilityResponseText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "chat content",
			body: `{"choices":[{"message":{"content":"<svg></svg>"}}]}`,
			want: "<svg></svg>",
		},
		{
			name: "responses output content",
			body: `{"output":[{"type":"message","content":[{"type":"output_text","text":"<svg"},{"type":"output_text","text":"></svg>"}]}]}`,
			want: "<svg></svg>",
		},
		{
			name: "responses output text convenience field",
			body: `{"output_text":"<svg></svg>"}`,
			want: "<svg></svg>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capabilityResponseText([]byte(tt.body)); got != tt.want {
				t.Fatalf("capabilityResponseText() = %q, want %q", got, tt.want)
			}
		})
	}
}
