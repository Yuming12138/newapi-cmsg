package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestNormalizeCodexSparkReasoningContext(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		body      string
		wantValue string
		wantRaw   string
	}{
		{
			name:      "spark downgrades all turns to auto",
			model:     "gpt-5.3-codex-spark",
			body:      `{"reasoning":{"context":"all_turns"}}`,
			wantValue: "auto",
		},
		{
			name:      "spark model comparison is case insensitive",
			model:     " GPT-5.3-CODEX-SPARK ",
			body:      `{"reasoning":{"context":"all_turns"}}`,
			wantValue: "auto",
		},
		{
			name:      "spark preserves current turn",
			model:     "gpt-5.3-codex-spark",
			body:      `{"reasoning":{"context":"current_turn"}}`,
			wantValue: "current_turn",
		},
		{
			name:      "spark preserves auto",
			model:     "gpt-5.3-codex-spark",
			body:      `{"reasoning":{"context":"auto"}}`,
			wantValue: "auto",
		},
		{
			name:    "other models remain untouched",
			model:   "gpt-5.5",
			body:    `{"reasoning":{"context":"all_turns"}}`,
			wantRaw: `{"reasoning":{"context":"all_turns"}}`,
		},
		{
			name:    "missing context remains untouched",
			model:   "gpt-5.3-codex-spark",
			body:    `{"reasoning":{"effort":"high"}}`,
			wantRaw: `{"reasoning":{"effort":"high"}}`,
		},
		{
			name:    "non string context remains untouched",
			model:   "gpt-5.3-codex-spark",
			body:    `{"reasoning":{"context":{"turn":"turn-1"}}}`,
			wantRaw: `{"reasoning":{"context":{"turn":"turn-1"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCodexSparkReasoningContext([]byte(tt.body), tt.model)
			if tt.wantRaw != "" {
				if string(got) != tt.wantRaw {
					t.Fatalf("body = %s, want unchanged %s", got, tt.wantRaw)
				}
				return
			}
			if gotValue := gjson.GetBytes(got, "reasoning.context").String(); gotValue != tt.wantValue {
				t.Fatalf("reasoning.context = %q, want %q; body=%s", gotValue, tt.wantValue, got)
			}
		})
	}
}

func TestCodexExecutorExecuteStreamNormalizesSparkAllTurnsInFinalRequest(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"background\":false,\"error\":null}}\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	result, err := executor.ExecuteStream(context.Background(), newCodexSignatureTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex-spark",
		Payload: []byte(`{"model":"gpt-5.3-codex-spark","stream":true,"input":[{"role":"user","content":"hello"}],"reasoning":{"context":"all_turns"}}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for range result.Chunks {
	}

	if got := gjson.GetBytes(gotBody, "reasoning.context").String(); got != "auto" {
		t.Fatalf("final reasoning.context = %q, want auto; body=%s", got, gotBody)
	}
}
