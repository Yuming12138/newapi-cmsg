package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const codexNamespacedHistoryPayload = `{
  "model":"gpt-5.6-sol",
  "tools":[
    {"type":"namespace","name":"alpha","tools":[{"type":"custom","name":"shell"}]},
    {"type":"namespace","name":"beta","tools":[{"type":"custom","name":"shell"}]}
  ],
  "input":[
    {"type":"function_call","call_id":"fn-a","name":"lookup","arguments":"{}","namespace":"alpha"},
    {"type":"function_call_output","call_id":"fn-a","output":"ok","namespace":"alpha"},
    {"type":"custom_tool_call","call_id":"custom-a","name":"shell","input":"one","namespace":"alpha"},
    {"type":"custom_tool_call_output","call_id":"custom-a","output":"one-ok","namespace":"alpha"},
    {"type":"custom_tool_call","call_id":"custom-b","name":"shell","input":"two","namespace":"beta"},
    {"type":"custom_tool_call_output","call_id":"custom-b","output":"two-ok","namespace":"beta"}
  ]
}`

func TestNormalizeCodexNamespacedCustomToolHistory(t *testing.T) {
	original := []byte(codexNamespacedHistoryPayload)
	got := normalizeCodexNamespacedCustomToolHistory(bytes.Clone(original))
	assertCodexNamespacedHistoryNormalized(t, got)

	if gotTools, wantTools := gjson.GetBytes(got, "tools").Raw, gjson.GetBytes(original, "tools").Raw; gotTools != wantTools {
		t.Fatalf("namespace tool declarations changed:\ngot=%s\nwant=%s", gotTools, wantTools)
	}
	if gotNamespace := gjson.GetBytes(got, "input.0.namespace").String(); gotNamespace != "alpha" {
		t.Fatalf("function_call namespace = %q, want alpha", gotNamespace)
	}
	if gotNamespace := gjson.GetBytes(got, "input.1.namespace").String(); gotNamespace != "alpha" {
		t.Fatalf("function_call_output namespace = %q, want alpha", gotNamespace)
	}
}

func TestCodexExecutorNormalizesNamespacedCustomToolHistoryAcrossHTTPPaths(t *testing.T) {
	tests := []struct {
		name   string
		alt    string
		stream bool
	}{
		{name: "execute"},
		{name: "execute stream", stream: true},
		{name: "compact", alt: "responses/compact"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capturedBody := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				capturedBody <- body
				if tc.alt == "responses/compact" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"resp-compact","object":"response.compaction","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0,\"total_tokens\":1}}}\n\n"))
			}))
			defer server.Close()

			exec := NewCodexExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
			auth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": server.URL}}
			req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(codexNamespacedHistoryPayload)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response"), Alt: tc.alt, Stream: tc.stream}

			if tc.stream {
				result, errExecute := exec.ExecuteStream(context.Background(), auth, req, opts)
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatalf("stream chunk error = %v", chunk.Err)
					}
				}
			} else if _, errExecute := exec.Execute(context.Background(), auth, req, opts); errExecute != nil {
				t.Fatalf("Execute() error = %v", errExecute)
			}

			select {
			case body := <-capturedBody:
				assertCodexNamespacedHistoryNormalized(t, body)
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for captured Codex HTTP body")
			}
		})
	}
}

func TestCodexWebsocketsExecutorNormalizesNamespacedCustomToolHistory(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "execute"
		if stream {
			name = "execute stream"
		}
		t.Run(name, func(t *testing.T) {
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			capturedBody := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, errUpgrade := upgrader.Upgrade(w, r, nil)
				if errUpgrade != nil {
					t.Errorf("upgrade websocket: %v", errUpgrade)
					return
				}
				defer func() { _ = conn.Close() }()

				_, payload, errRead := conn.ReadMessage()
				if errRead != nil {
					t.Errorf("read websocket request: %v", errRead)
					return
				}
				capturedBody <- bytes.Clone(payload)
				completed := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`)
				if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
					t.Errorf("write websocket response: %v", errWrite)
				}
			}))
			defer server.Close()

			exec := NewCodexWebsocketsExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
			auth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": server.URL}}
			req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(codexNamespacedHistoryPayload)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex"), Stream: stream}

			if stream {
				result, errExecute := exec.ExecuteStream(context.Background(), auth, req, opts)
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatalf("stream chunk error = %v", chunk.Err)
					}
				}
			} else if _, errExecute := exec.Execute(context.Background(), auth, req, opts); errExecute != nil {
				t.Fatalf("Execute() error = %v", errExecute)
			}

			select {
			case body := <-capturedBody:
				assertCodexNamespacedHistoryNormalized(t, body)
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for captured Codex websocket body")
			}
		})
	}
}

func assertCodexNamespacedHistoryNormalized(t *testing.T, body []byte) {
	t.Helper()

	if got := gjson.GetBytes(body, "tools.0.type").String(); got != "namespace" {
		t.Fatalf("tools.0.type = %q, want namespace; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "tools.1.type").String(); got != "namespace" {
		t.Fatalf("tools.1.type = %q, want namespace; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.0.namespace").String(); got != "alpha" {
		t.Fatalf("function_call namespace = %q, want alpha; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "input.1.namespace").String(); got != "alpha" {
		t.Fatalf("function_call_output namespace = %q, want alpha; body=%s", got, body)
	}

	for _, index := range []int{2, 3, 4, 5} {
		if namespace := gjson.GetBytes(body, "input."+strconv.Itoa(index)+".namespace"); namespace.Exists() {
			t.Fatalf("input.%d namespace was not removed; body=%s", index, body)
		}
	}
	if got := gjson.GetBytes(body, "input.2.call_id").String(); got != "custom-a" {
		t.Fatalf("first custom call_id = %q, want custom-a", got)
	}
	if got := gjson.GetBytes(body, "input.4.call_id").String(); got != "custom-b" {
		t.Fatalf("second custom call_id = %q, want custom-b", got)
	}
	if got := gjson.GetBytes(body, "input.2.name").String(); got != "shell" {
		t.Fatalf("first custom name = %q, want shell", got)
	}
	if got := gjson.GetBytes(body, "input.4.name").String(); got != "shell" {
		t.Fatalf("second custom name = %q, want shell", got)
	}
}
