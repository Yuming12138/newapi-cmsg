package helps

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	geminicommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
)

func TestCompactAntigravityTokenLimitRetry(t *testing.T) {
	text := strings.Repeat("+-[];=", 200000)
	payload := []byte(fmt.Sprintf(`{"request":{"contents":[{"role":"user","parts":[{"text":%q}]}]}}`, text))
	req, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status int
		model  string
		body   string
		want   bool
	}{
		{"token limit", 400, "gemini-3.8-flash", `{"error":{"message":"The input token count exceeds the maximum number of tokens allowed 1048576."}}`, true},
		{"plain token limit", 400, "gemini-3.8-flash", "The input token count exceeds the maximum number of tokens allowed 1048576.", true},
		{"unrelated 400", 400, "gemini-3.8-flash", `{"error":{"message":"Invalid thought signature"}}`, false},
		{"quota error", 429, "gemini-3.8-flash", "The input token count exceeds the maximum number of tokens allowed 1048576.", false},
		{"successful response", 200, "gemini-3.8-flash", "The input token count exceeds the maximum number of tokens allowed 1048576.", false},
		{"non-Gemini model", 400, "claude-opus", "The input token count exceeds the maximum number of tokens allowed 1048576.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, ok := CompactAntigravityTokenLimitRetry(tc.status, []byte(tc.body), req, tc.model)
			if ok != tc.want {
				t.Fatalf("retry = %t, want %t", ok, tc.want)
			}
			if ok {
				before := geminicommon.EstimateGeminiPayloadTokens(payload, "request.contents")
				after := geminicommon.EstimateGeminiPayloadTokens(result, "request.contents")
				if bytes.Equal(result, payload) || after > before/2 {
					t.Fatalf("retry must really shrink the sent body: before=%d after=%d", before, after)
				}
			}
		})
	}
}

func TestCompactAntigravityTokenLimitRetry_UnshrinkableDoesNotRetry(t *testing.T) {
	payload := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"short"}]}]}}`)
	req, _ := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewReader(payload))
	if _, ok := CompactAntigravityTokenLimitRetry(400, []byte("The input token count exceeds the maximum number of tokens allowed 1048576."), req, "gemini-3.8-flash"); ok {
		t.Fatal("must not retry an identical unshrinkable request")
	}
}
