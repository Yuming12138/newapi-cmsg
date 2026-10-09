package common

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestEstimateTextTokens(t *testing.T) {
	if got := EstimateTextTokens(""); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := EstimateTextTokens("a"); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}

	// 300 ascii chars -> ~100 tokens
	text300 := strings.Repeat("abcd ", 60)
	got := EstimateTextTokens(text300)
	if got < 90 || got > 120 {
		t.Fatalf("expected between 90 and 120 tokens, got %d", got)
	}

	// Chinese characters (100 chars -> ~150 tokens)
	cjk := strings.Repeat("你好世界测试", 20)
	gotCJK := EstimateTextTokens(cjk)
	if gotCJK < 140 || gotCJK > 180 {
		t.Fatalf("expected between 140 and 180 tokens, got %d", gotCJK)
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
			{"role": "user", "parts": [{"functionResponse": {"name": "test_1", "response": {"result": "%s"}}}]},
			{"role": "model", "parts": [{"functionCall": {"name": "test_2", "args": {}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "test_2", "response": {"result": "%s"}}}]}
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
