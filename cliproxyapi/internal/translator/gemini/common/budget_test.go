package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

func TestEstimateTextTokens(t *testing.T) {
	if got := EstimateTextTokens(""); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := EstimateTextTokens("a"); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}

	// Code-safe estimate: 300 ASCII bytes -> 150 tokens, not the old 100.
	text300 := strings.Repeat("abcd ", 60)
	got := EstimateTextTokens(text300)
	if got != 150 {
		t.Fatalf("expected 150 tokens, got %d", got)
	}

	// Chinese characters (120 chars -> 180 tokens)
	cjk := strings.Repeat("你好世界测试", 20)
	gotCJK := EstimateTextTokens(cjk)
	if gotCJK != 180 {
		t.Fatalf("expected 180 tokens, got %d", gotCJK)
	}
}

func TestCompactHistoricalToolOutputs(t *testing.T) {
	// 3 turns:
	// Turn 0: user message
	// Turn 1: user tool response with large characters
	// Turn 2: active turn (last turn) with tool response
	largeToolResult := strings.Repeat("abcdef0123456789", 3000) // ~48KB
	activeToolResult := strings.Repeat("xyz0123456789012", 500) // ~8KB

	payload := fmt.Sprintf(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "Please inspect system."}]
			},
			{
				"role": "user",
				"parts": [{
					"functionResponse": {
						"name": "exec",
						"response": {"result": "%s"}
					}
				}]
			},
			{
				"role": "user",
				"parts": [{
					"functionResponse": {
						"name": "exec",
						"response": {"result": "%s"}
					}
				}]
			}
		]
	}`, largeToolResult, activeToolResult)

	compacted, modified := CompactHistoricalToolOutputs([]byte(payload), "contents")
	if !modified {
		t.Fatal("expected modified to be true")
	}

	// Turn 1 (historical) must be truncated
	t1Result := gjson.GetBytes(compacted, "contents.1.parts.0.functionResponse.response.result").String()
	if !strings.Contains(t1Result, TruncatedToolOutputNotice) {
		t.Fatalf("expected truncation notice in turn 1, got: %s", t1Result)
	}
	if len(t1Result) > MaxHistoricalToolOutputLen+len(TruncatedToolOutputNotice)+100 {
		t.Fatalf("turn 1 result too long: %d", len(t1Result))
	}
	if !strings.HasPrefix(t1Result, largeToolResult[:HistoricalToolOutputHeadLen]) {
		t.Fatal("turn 1 prefix mismatch")
	}
	if !strings.HasSuffix(t1Result, largeToolResult[len(largeToolResult)-HistoricalToolOutputTailLen:]) {
		t.Fatal("turn 1 suffix mismatch")
	}

	// Turn 2 (last turn / active turn) must NOT be truncated
	t2Result := gjson.GetBytes(compacted, "contents.2.parts.0.functionResponse.response.result").String()
	if t2Result != activeToolResult {
		t.Fatalf("expected turn 2 to be unmodified, got length %d vs %d", len(t2Result), len(activeToolResult))
	}
}

func TestPruneOldestTurns_ConversationalTurns(t *testing.T) {
	// Create multiple conversational turn blocks:
	// Turn 0: user "Turn 0 prompt"
	// Turn 1: model "Turn 1 answer"
	// Turn 2: user "Turn 2 prompt"
	// Turn 3: model "Turn 3 answer"
	// Turn 4: user "Turn 4 prompt"
	largeText := strings.Repeat("This is a detailed analysis of the problem. ", 1000)

	payload := fmt.Sprintf(`{
		"contents": [
			{"role": "user", "parts": [{"text": "Turn 0 %s"}]},
			{"role": "model", "parts": [{"text": "Turn 1 %s"}]},
			{"role": "user", "parts": [{"text": "Turn 2 %s"}]},
			{"role": "model", "parts": [{"text": "Turn 3 %s"}]},
			{"role": "user", "parts": [{"text": "Turn 4 %s"}]}
		]
	}`, largeText, largeText, largeText, largeText, largeText)

	targetMaxTokens := 20000

	pruned := PruneOldestTurns([]byte(payload), "contents", targetMaxTokens)

	firstRole := gjson.GetBytes(pruned, "contents.0.role").String()
	if firstRole != "user" {
		t.Fatalf("expected first role to be 'user', got %s", firstRole)
	}

	firstText := gjson.GetBytes(pruned, "contents.0.parts.0.text").String()
	if !strings.Contains(firstText, TruncatedHistoryNotice) {
		t.Fatalf("expected truncation notice in first turn, got: %s", firstText)
	}

	tokens := EstimateGeminiPayloadTokens(pruned, "contents")
	if tokens > targetMaxTokens {
		t.Fatalf("expected tokens <= %d, got %d", targetMaxTokens, tokens)
	}
}

func TestPruneOldestTurns_ToolCallResponsePairs(t *testing.T) {
	// 1 user goal + multiple tool pairs:
	// Turn 0: user "Please run tests and fix"
	// Turn 1: model functionCall "test_1"
	// Turn 2: user functionResponse "fail_1"
	// Turn 3: model functionCall "test_2"
	// Turn 4: user functionResponse "pass_2"
	largeOutput := strings.Repeat("Log line with build output and trace information.\n", 500)

	payload := fmt.Sprintf(`{
		"contents": [
			{"role": "user", "parts": [{"text": "Please run tests and fix."}]},
			{"role": "model", "parts": [{"functionCall": {"name": "test_1", "args": {}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "test_1", "response": {"result": %q}}}]},
			{"role": "model", "parts": [{"functionCall": {"name": "test_2", "args": {}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "test_2", "response": {"result": %q}}}]}
		]
	}`, largeOutput, largeOutput)

	targetMaxTokens := 10000
	pruned := PruneOldestTurns([]byte(payload), "contents", targetMaxTokens)

	firstRole := gjson.GetBytes(pruned, "contents.0.role").String()
	firstText := gjson.GetBytes(pruned, "contents.0.parts.0.text").String()
	if firstRole != "user" {
		t.Fatalf("expected first role 'user', got %s", firstRole)
	}
	if firstText != "Please run tests and fix." {
		t.Fatalf("expected first turn goal preserved, got: %s", firstText)
	}

	secondRole := gjson.GetBytes(pruned, "contents.1.role").String()
	if secondRole != "model" {
		t.Fatalf("expected second role 'model', got %s", secondRole)
	}
	callName := gjson.GetBytes(pruned, "contents.1.parts.0.functionCall.name").String()
	if callName != "test_2" {
		t.Fatalf("expected test_2 call, got %s", callName)
	}
}

func TestEnsureAntigravityBudget_FastPath(t *testing.T) {
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"Hello world"}]}]}`)
	res := EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	if string(payload) != string(res) {
		t.Fatalf("expected payload unchanged, got %s", string(res))
	}
}

func TestEnsureAntigravityBudget_RequestContentsPath(t *testing.T) {
	payload := []byte(`{"project":"my-proj","request":{"contents":[{"role":"user","parts":[{"text":"Hello"}]}]}}`)
	res := EnsureAntigravityBudget(payload, "request.contents", "gemini-3.8-flash")
	if string(payload) != string(res) {
		t.Fatalf("expected payload unchanged, got %s", string(res))
	}
}

func TestEnsureAntigravityBudget_ActiveToolResponse(t *testing.T) {
	for _, path := range []string{"contents", "request.contents"} {
		for _, responseKey := range []string{"functionResponse", "function_response"} {
			t.Run(path+"/"+responseKey, func(t *testing.T) {
				output := strings.Repeat("+/-=<>[]{},.:;0123456789", 80000)
				payload := fmt.Sprintf(`{"contents":[
					{"role":"user","parts":[{"text":"Inspect the command output and continue."}]},
					{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"exec","args":{"cmd":"build"}},"thoughtSignature":"signed-call-do-not-modify"}]},
					{"role":"user","parts":[{%q:{"id":"call_1","name":"exec","response":{"result":%q}}}]}]}`, responseKey, output)
				if path == "request.contents" {
					payload = `{"request":` + payload + `}`
				}
				beforeCall := gjson.Get(payload, path+".1").Raw
				result := EnsureAntigravityBudget([]byte(payload), path, "gemini-3.8-flash-tiered")
				if !json.Valid(result) {
					t.Fatal("compacted payload must be valid JSON")
				}
				if tokens := EstimateGeminiPayloadTokens(result, path); tokens > AntigravityDefaultMaxTokens {
					t.Fatalf("active tool output remained oversized: %d tokens", tokens)
				}
				if got := gjson.GetBytes(result, path+".1").Raw; got != beforeCall {
					t.Fatal("active signed call must remain byte-for-byte unchanged")
				}
				response := gjson.GetBytes(result, path+".2.parts.0."+responseKey)
				if response.Get("id").String() != "call_1" || response.Get("name").String() != "exec" {
					t.Fatal("tool pairing identifiers were lost")
				}
				text := response.Get("response.result").String()
				if !strings.Contains(text, TruncatedToolOutputNotice) || !strings.HasPrefix(text, output[:8000]) || !strings.HasSuffix(text, output[len(output)-8000:]) {
					t.Fatal("active output should retain head, tail and explicit notice")
				}
				if again := EnsureAntigravityBudget(result, path, "gemini-3.8-flash-tiered"); !bytes.Equal(result, again) {
					t.Fatal("compaction must be idempotent")
				}
			})
		}
	}
}

func TestEnsureAntigravityBudget_BelowOldFastPath(t *testing.T) {
	text := strings.Repeat("+-[];=", 270000) // 1.62MB, skipped by the old 1.8MB gate.
	payload := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":%q}]}]}`, text))
	result := EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	if bytes.Equal(payload, result) {
		t.Fatal("potentially oversized code must not skip budget checks")
	}
	if tokens := EstimateGeminiPayloadTokens(result, "contents"); tokens > AntigravityDefaultMaxTokens {
		t.Fatalf("still over budget: %d", tokens)
	}
	if !strings.Contains(gjson.GetBytes(result, "contents.0.parts.0.text").String(), TruncatedMessageNotice) {
		t.Fatal("plain-message fallback must be explicit")
	}
}

func TestEnsureAntigravityBudget_SingleTurnMultipleTextParts(t *testing.T) {
	large := strings.Repeat("中文😀abc", 150000)
	payload := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"Keep this instruction intact."},{"text":%q},{"text":%q}]}]}`, large, large))
	result := EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	if got := gjson.GetBytes(result, "contents.0.parts.0.text").String(); got != "Keep this instruction intact." {
		t.Fatal("short user instruction was changed")
	}
	for i := 1; i <= 2; i++ {
		text := gjson.GetBytes(result, fmt.Sprintf("contents.0.parts.%d.text", i)).String()
		if !utf8.ValidString(text) || !strings.Contains(text, TruncatedMessageNotice) || strings.ContainsRune(text, utf8.RuneError) {
			t.Fatal("text truncation must preserve UTF-8 and show a notice")
		}
	}
	if tokens := EstimateGeminiPayloadTokens(result, "contents"); tokens > AntigravityDefaultMaxTokens {
		t.Fatalf("multipart request still over budget: %d", tokens)
	}
}

func TestCompactHistoricalToolOutputs_StructuredResponseAndUTF8(t *testing.T) {
	text := strings.Repeat("日志😀", 3000)
	payload := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"functionResponse":{"id":"c","name":"exec","response":{"output":%q,"exit_code":1}}}]},{"role":"user","parts":[{"text":"continue"}]}]}`, text))
	result, changed := CompactHistoricalToolOutputs(payload, "contents")
	if !changed || !json.Valid(result) {
		t.Fatal("structured output should be compacted as valid JSON")
	}
	response := gjson.GetBytes(result, "contents.0.parts.0.functionResponse.response")
	if !response.IsObject() {
		t.Fatal("functionResponse.response must remain an object")
	}
	if got := response.Get("result").String(); !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatal("UTF-8 was corrupted")
	}
}

func TestEstimateGeminiPayloadTokens_IncludesFixedInputsAndResponseSiblings(t *testing.T) {
	text := strings.Repeat("abc", 1000)
	payload := []byte(fmt.Sprintf(`{"request":{"system_instruction":{"parts":[{"text":%q}]},"tools":[{"description":%q}],"generationConfig":{"responseSchema":{"description":%q}},"contents":[{"parts":[{"functionResponse":{"name":"exec","response":{"result":"ok","extra":%q}}}]}]}}`, text, text, text, text))
	if tokens := EstimateGeminiPayloadTokens(payload, "request.contents"); tokens < 6000 {
		t.Fatalf("large fixed inputs/response siblings were missed: %d", tokens)
	}
}

func TestEnsureAntigravityBudget_ActiveStructuredResponseSiblings(t *testing.T) {
	output := strings.Repeat("xyz", 600000)
	payload := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"continue"}]},{"role":"model","parts":[{"functionCall":{"id":"c","name":"exec","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"c","name":"exec","response":{"result":"ok","logs":%q,"exit_code":0}}}]}]}`, output))
	result := EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	response := gjson.GetBytes(result, "contents.2.parts.0.functionResponse")
	if !response.Get("response").IsObject() || response.Get("id").String() != "c" || response.Get("name").String() != "exec" {
		t.Fatal("structured compaction must retain response envelope and identifiers")
	}
	if !strings.Contains(response.Get("response.result").String(), TruncatedToolOutputNotice) {
		t.Fatal("large response siblings were not compacted")
	}
	if tokens := EstimateGeminiPayloadTokens(result, "contents"); tokens > AntigravityDefaultMaxTokens {
		t.Fatalf("structured response still over budget: %d", tokens)
	}
}

func TestEnsureAntigravityBudget_UnshrinkableInputsArePreserved(t *testing.T) {
	payload := []byte(fmt.Sprintf(`{"systemInstruction":{"parts":[{"text":%q}]},"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, strings.Repeat("s", 1600000)))
	result := EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	if !bytes.Equal(payload, result) {
		t.Fatal("system instructions must not be silently rewritten")
	}
	if EstimateGeminiPayloadTokens(result, "contents") <= AntigravityDefaultMaxTokens {
		t.Fatal("executor needs an over-budget signal for unshrinkable inputs")
	}
}

func TestPruneOldestTurns_DoesNotDropUnmatchedToolPair(t *testing.T) {
	payload := []byte(fmt.Sprintf(`{"contents":[
		{"role":"user","parts":[{"text":"goal"}]},
		{"role":"model","parts":[{"functionCall":{"name":"exec","id":"a","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"exec","id":"different","response":{"result":%q}}}]},
		{"role":"model","parts":[{"functionCall":{"name":"exec","id":"b","args":{}}}]},
		{"role":"user","parts":[{"functionResponse":{"name":"exec","id":"b","response":{"result":"ok"}}}]}]}`, strings.Repeat("x", 60000)))
	result := PruneOldestTurns(payload, "contents", 10000)
	if !bytes.Equal(result, payload) {
		t.Fatal("unmatched pairs must not be silently removed")
	}
}

func BenchmarkEnsureAntigravityBudget_SmallRequest(b *testing.B) {
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	b.ReportAllocs()
	for b.Loop() {
		EnsureAntigravityBudget(payload, "contents", "gemini-3.8-flash")
	}
}

func BenchmarkEnsureAntigravityBudget_LongToolHistory(b *testing.B) {
	var payload strings.Builder
	payload.WriteString(`{"contents":[{"role":"user","parts":[{"text":"run tests"}]}`)
	output := strings.Repeat("build log info abcdef0123456789 ", 1000)
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&payload, `,{"role":"model","parts":[{"functionCall":{"name":"exec","id":"%d","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"exec","id":"%d","response":{"result":%q}}}]}`, i, i, output)
	}
	payload.WriteString(`]}`)
	raw := []byte(payload.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for b.Loop() {
		EnsureAntigravityBudget(raw, "contents", "gemini-3.8-flash")
	}
}
