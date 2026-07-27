package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestNormalizeCodexSparkReasoning(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		body              string
		wantContext       string
		wantSummaryExists bool
		wantRaw           string
	}{
		{
			name:        "spark downgrades all turns and removes summary",
			model:       "gpt-5.3-codex-spark",
			body:        `{"reasoning":{"context":"all_turns","summary":"detailed"}}`,
			wantContext: "auto",
		},
		{
			name:        "spark model comparison is case insensitive",
			model:       " GPT-5.3-CODEX-SPARK ",
			body:        `{"reasoning":{"context":"all_turns","summary":"auto"}}`,
			wantContext: "auto",
		},
		{
			name:        "spark preserves current turn and removes summary",
			model:       "gpt-5.3-codex-spark",
			body:        `{"reasoning":{"context":"current_turn","summary":"concise"}}`,
			wantContext: "current_turn",
		},
		{
			name:        "spark removes summary without context",
			model:       "gpt-5.3-codex-spark",
			body:        `{"reasoning":{"effort":"high","summary":"detailed"}}`,
			wantContext: "",
		},
		{
			name:              "other models remain untouched",
			model:             "gpt-5.5",
			body:              `{"reasoning":{"context":"all_turns","summary":"detailed"}}`,
			wantRaw:           `{"reasoning":{"context":"all_turns","summary":"detailed"}}`,
			wantSummaryExists: true,
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
			got := normalizeCodexSparkReasoning([]byte(tt.body), tt.model)
			if tt.wantRaw != "" {
				if string(got) != tt.wantRaw {
					t.Fatalf("body = %s, want unchanged %s", got, tt.wantRaw)
				}
				return
			}
			if gotContext := gjson.GetBytes(got, "reasoning.context").String(); gotContext != tt.wantContext {
				t.Fatalf("reasoning.context = %q, want %q; body=%s", gotContext, tt.wantContext, got)
			}
			if gotSummaryExists := gjson.GetBytes(got, "reasoning.summary").Exists(); gotSummaryExists != tt.wantSummaryExists {
				t.Fatalf("reasoning.summary exists = %v, want %v; body=%s", gotSummaryExists, tt.wantSummaryExists, got)
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
		Payload: []byte(`{"model":"gpt-5.3-codex-spark","stream":true,"input":[{"role":"user","content":"hello"}],"reasoning":{"context":"all_turns","summary":"detailed"}}`),
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
	if got := gjson.GetBytes(gotBody, "reasoning.summary"); got.Exists() {
		t.Fatalf("final reasoning.summary must be removed; body=%s", gotBody)
	}
}

func TestCodexWebsocketsExecuteNormalizesSparkReasoningInFinalRequest(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	capturedPayload := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, errUpgrade := upgrader.Upgrade(w, r, nil)
		if errUpgrade != nil {
			t.Errorf("upgrade websocket: %v", errUpgrade)
			return
		}
		defer func() { _ = conn.Close() }()

		_, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			t.Errorf("read upstream websocket message: %v", errRead)
			return
		}
		capturedPayload <- bytes.Clone(payload)

		completed := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
		if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
			t.Errorf("write completed websocket message: %v", errWrite)
		}
	}))
	defer server.Close()

	executor := NewCodexWebsocketsExecutor(&config.Config{})
	_, errExecute := executor.Execute(context.Background(), newCodexSignatureTestAuth(server.URL), cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex-spark",
		Payload: []byte(`{"model":"gpt-5.3-codex-spark","input":[{"role":"user","content":"hello"}],"reasoning":{"context":"all_turns","summary":"detailed"}}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex")})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}

	select {
	case payload := <-capturedPayload:
		if got := gjson.GetBytes(payload, "reasoning.context").String(); got != "auto" {
			t.Fatalf("websocket reasoning.context = %q, want auto; body=%s", got, payload)
		}
		if got := gjson.GetBytes(payload, "reasoning.summary"); got.Exists() {
			t.Fatalf("websocket reasoning.summary must be removed; body=%s", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket payload")
	}
}
