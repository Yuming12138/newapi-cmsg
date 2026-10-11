package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	geminicommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestAntigravityBuildRequest_ActiveToolOutputBudget(t *testing.T) {
	text := strings.Repeat("+-[];=", 300000)
	payload := []byte(fmt.Sprintf(`{"request":{"contents":[
		{"role":"user","parts":[{"text":"Inspect and continue"}]},
		{"role":"model","parts":[{"functionCall":{"id":"c","name":"exec","args":{}},"thoughtSignature":"skip_thought_signature_validator"}]},
		{"role":"user","parts":[{"functionResponse":{"id":"c","name":"exec","response":{"result":%q}}}]}]}}`, text))
	exec := NewAntigravityExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"project_id": "test-project"}}
	req, err := exec.buildRequest(context.Background(), auth, "test-token", "gemini-3.8-flash", payload, true, "", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer req.Body.Close()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if tokens := geminicommon.EstimateGeminiPayloadTokens(body, "request.contents"); tokens > geminicommon.AntigravityDefaultMaxTokens {
		t.Fatalf("outgoing body still oversized: %d", tokens)
	}
	if text := gjson.GetBytes(body, "request.contents.2.parts.0.functionResponse.response.result").String(); !strings.Contains(text, geminicommon.TruncatedToolOutputNotice) {
		t.Fatal("last tool output was not compacted before sending")
	}
}

func TestAntigravityBuildRequest_RejectsUnshrinkableBudget(t *testing.T) {
	for _, field := range []string{"systemInstruction", "tools", "functionCall"} {
		t.Run(field, func(t *testing.T) {
			large := strings.Repeat("x", 1600000)
			var payload string
			switch field {
			case "systemInstruction":
				payload = fmt.Sprintf(`{"request":{"systemInstruction":{"parts":[{"text":%q}]},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`, large)
			case "tools":
				payload = fmt.Sprintf(`{"request":{"tools":[{"functionDeclarations":[{"name":"exec","description":%q,"parameters":{"type":"object"}}]}],"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`, large)
			case "functionCall":
				payload = fmt.Sprintf(`{"request":{"contents":[{"role":"user","parts":[{"text":"goal"}]},{"role":"model","parts":[{"functionCall":{"id":"c","name":"exec","args":{"input":%q}}}]},{"role":"user","parts":[{"functionResponse":{"id":"c","name":"exec","response":{"result":"ok"}}}]}]}}`, large)
			}
			exec := NewAntigravityExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Metadata: map[string]any{"project_id": "test-project"}}
			req, err := exec.buildRequest(context.Background(), auth, "test-token", "gemini-3.8-flash", []byte(payload), true, "", "https://example.com")
			var status statusErr
			if req != nil || !errors.As(err, &status) || status.StatusCode() != 400 || !strings.Contains(status.msg, "context_length_exceeded") {
				t.Fatalf("unshrinkable context must be rejected locally, got req=%v err=%v", req != nil, err)
			}
		})
	}
}

func TestAntigravityTokenLimitRecovery(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash", "gemini-3-pro"} {
		for _, stream := range []bool{false, true} {
			for _, kind := range []string{"recovered", "persistent", "unrelated"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", model, stream, kind), func(t *testing.T) {
					var sent [][]byte
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						sent = append(sent, body)
						if kind == "unrelated" {
							w.WriteHeader(400)
							_, _ = io.WriteString(w, `{"error":{"message":"Invalid thought signature"}}`)
							return
						}
						if kind == "persistent" || len(sent) == 1 {
							w.WriteHeader(400)
							_, _ = io.WriteString(w, `{"error":{"message":"The input token count exceeds the maximum number of tokens allowed 1048576."}}`)
							return
						}
						if stream || strings.Contains(model, "gemini-3-pro") {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}}\n\n")
						} else {
							_, _ = io.WriteString(w, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}`)
						}
					}))
					defer server.Close()
					exec := NewAntigravityExecutor(&config.Config{RequestRetry: 5})
					auth := antigravityStreamTestAuth("budget-"+t.Name(), server.URL, "test-token")
					text := strings.Repeat("+-[];=", 200000)
					req := cliproxyexecutor.Request{Model: model, Payload: []byte(fmt.Sprintf(`{"request":{"contents":[{"role":"user","parts":[{"text":%q}]}]}}`, text))}
					opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity}
					var err error
					if stream {
						var result *cliproxyexecutor.StreamResult
						result, err = exec.ExecuteStream(context.Background(), auth, req, opts)
						if err == nil {
							for chunk := range result.Chunks {
								if chunk.Err != nil {
									t.Fatal(chunk.Err)
								}
							}
						}
					} else {
						_, err = exec.Execute(context.Background(), auth, req, opts)
					}
					if (err == nil) != (kind == "recovered") {
						t.Fatalf("unexpected recovery outcome: %v", err)
					}
					wantCalls := 2
					if kind == "unrelated" {
						wantCalls = 1
					}
					if len(sent) != wantCalls {
						t.Fatalf("requests=%d, want %d (one recovery maximum)", len(sent), wantCalls)
					}
					if wantCalls == 2 {
						before := geminicommon.EstimateGeminiPayloadTokens(sent[0], "request.contents")
						after := geminicommon.EstimateGeminiPayloadTokens(sent[1], "request.contents")
						if after > before/2 {
							t.Fatalf("sent retry was not actually smaller: before=%d after=%d", before, after)
						}
					}
				})
			}
		}
	}
}
