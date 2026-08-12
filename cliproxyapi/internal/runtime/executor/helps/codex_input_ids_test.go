package helps

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSanitizeCodexInputItemIDsBoundaries(t *testing.T) {
	id64 := strings.Repeat("a", 64)
	id65 := strings.Repeat("b", 65)
	unicode65 := strings.Repeat("界", 65)
	body := []byte(`{"input":[{"id":"` + id64 + `"},{"id":"` + id65 + `"},{"id":"` + unicode65 + `"}]}`)

	got := SanitizeCodexInputItemIDs(body)

	if actual := gjson.GetBytes(got, "input.0.id").String(); actual != id64 {
		t.Fatalf("64-character ID changed: %q", actual)
	}
	for _, path := range []string{"input.1.id", "input.2.id"} {
		actual := gjson.GetBytes(got, path).String()
		if len([]rune(actual)) != 64 {
			t.Fatalf("%s length = %d, want 64: %q", path, len([]rune(actual)), actual)
		}
	}
}

func TestSanitizeCodexInputItemIDsNormalizesResponseItemIDs(t *testing.T) {
	body := []byte(`{"input":[` +
		`{"type":"message","id":"item_e22c64c4a475595bd304a335","role":"assistant","status":"completed","content":[{"type":"output_text","text":"from grok"}]},` +
		`{"type":"message","id":"550e8400-e29b-41d4-a716-446655440000","role":"user","content":"next"},` +
		`{"type":"message","id":"out-1","role":"assistant","content":"reply"},` +
		`{"type":"message","id":42,"role":"user","content":"numeric"},` +
		`{"type":"message","id":"","role":"user","content":"empty"},` +
		`{"type":"message","id":"msg_valid","role":"assistant","content":"keep underscore"},` +
		`{"type":"message","id":"msg-valid","role":"assistant","content":"keep dash"},` +
		`{"type":"function_call","id":"item_call","call_id":"call-1","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","id":"item_output","call_id":"call-1","output":"ok"},` +
		`{"type":"item_reference","id":"item_reference"}` +
		`]}`)

	got := SanitizeCodexInputItemIDs(body)
	input := gjson.GetBytes(got, "input").Array()
	if len(input) != 10 {
		t.Fatalf("input length = %d, want 10: %s", len(input), got)
	}
	wantIDs := []string{
		"msg_item_e22c64c4a475595bd304a335",
		"msg_550e8400-e29b-41d4-a716-446655440000",
		"msg_out-1",
		"",
		"",
		"msg_valid",
		"msg-valid",
		"fc_item_call",
		"item_output",
		"item_reference",
	}
	for index, wantID := range wantIDs {
		gotID := input[index].Get("id")
		if wantID == "" {
			if gotID.Exists() && gotID.Type == gjson.String && gotID.String() != "" {
				t.Fatalf("input.%d.id = %q, want empty or absent", index, gotID.String())
			}
			continue
		}
		if gotID.String() != wantID {
			t.Fatalf("input.%d.id = %q, want %q", index, gotID.String(), wantID)
		}
	}
	if gotText := input[0].Get("content.0.text").String(); gotText != "from grok" {
		t.Fatalf("message content changed: %q", gotText)
	}
	if gotRole := input[0].Get("role").String(); gotRole != "assistant" {
		t.Fatalf("message role changed: %q", gotRole)
	}
	if gotStatus := input[0].Get("status").String(); gotStatus != "completed" {
		t.Fatalf("message status changed: %q", gotStatus)
	}
	if gotCallID := input[7].Get("call_id").String(); gotCallID != "call-1" {
		t.Fatalf("function call call_id changed: %q", gotCallID)
	}
	if gotCallID := input[8].Get("call_id").String(); gotCallID != "call-1" {
		t.Fatalf("function call output call_id changed: %q", gotCallID)
	}
	if gotID := input[9].Get("id").String(); gotID != "item_reference" {
		t.Fatalf("item reference id changed: %q", gotID)
	}
	if second := SanitizeCodexInputItemIDs(got); string(second) != string(got) {
		t.Fatalf("sanitizer is not idempotent: first=%s second=%s", got, second)
	}
}

func TestSanitizeCodexInputItemIDsAvoidsNormalizationCollisions(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		itemType string
		prefix   string
	}{
		{name: "message", itemType: "message", prefix: "msg_"},
		{name: "reasoning", itemType: "reasoning", prefix: "rs_"},
		{name: "function call", itemType: "function_call", prefix: "fc_"},
		{name: "custom tool call", itemType: "custom_tool_call", prefix: "ctc_"},
		{name: "custom tool call output", itemType: "custom_tool_call_output", prefix: "ctco_"},
	} {
		for _, idCase := range []struct {
			name      string
			invalidID string
		}{
			{name: "short", invalidID: "item_collision"},
			{name: "overlong", invalidID: strings.Repeat("x", codexInputItemIDLimit-len([]rune(testCase.prefix))+1)},
		} {
			prefixedID := testCase.prefix + idCase.invalidID
			for _, order := range []struct {
				name          string
				ids           [2]string
				prefixedIndex int
			}{
				{name: "local first", ids: [2]string{idCase.invalidID, prefixedID}, prefixedIndex: 1},
				{name: "prefixed first", ids: [2]string{prefixedID, idCase.invalidID}, prefixedIndex: 0},
			} {
				t.Run(testCase.name+"/"+idCase.name+"/"+order.name, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"input":[{"type":%q,"id":%q},{"type":%q,"id":%q}]}`, testCase.itemType, order.ids[0], testCase.itemType, order.ids[1]))

					first := SanitizeCodexInputItemIDs(body)
					second := SanitizeCodexInputItemIDs(body)
					normalizedAgain := SanitizeCodexInputItemIDs(first)
					ids := [2]string{
						gjson.GetBytes(first, "input.0.id").String(),
						gjson.GetBytes(first, "input.1.id").String(),
					}

					if ids[0] == ids[1] {
						t.Fatalf("distinct IDs collided after normalization: %q; payload=%s", ids[0], first)
					}
					for index, id := range ids {
						if !strings.HasPrefix(id, testCase.prefix) {
							t.Fatalf("input.%d.id = %q, want prefix %q", index, id, testCase.prefix)
						}
						if len([]rune(id)) > codexInputItemIDLimit {
							t.Fatalf("input.%d.id length = %d, want at most %d: %q", index, len([]rune(id)), codexInputItemIDLimit, id)
						}
					}
					if len([]rune(prefixedID)) <= codexInputItemIDLimit && ids[order.prefixedIndex] != prefixedID {
						t.Fatalf("existing valid ID changed: got %q want %q", ids[order.prefixedIndex], prefixedID)
					}
					if string(first) != string(second) {
						t.Fatalf("collision resolution is not deterministic: first=%s second=%s", first, second)
					}
					if string(first) != string(normalizedAgain) {
						t.Fatalf("collision resolution is not idempotent: first=%s normalized_again=%s", first, normalizedAgain)
					}
				})
			}
		}
	}
}

func TestSanitizeCodexInputItemIDsDropsOverlongEncryptedReasoningItem(t *testing.T) {
	longReasoningID := "rs_" + strings.Repeat("a", 64)
	shortReasoningID := "rs_" + strings.Repeat("b", 48)
	longCallID := strings.Repeat("call-item-", 8)
	body := []byte(`{"input":[` +
		`{"type":"message","id":"msg-1","role":"user","content":"before"},` +
		`{"type":"reasoning","id":"` + longReasoningID + `","encrypted_content":"gAAAA-encrypted","summary":[{"type":"summary_text","text":"drop me"}]},` +
		`{"type":"reasoning","id":"` + shortReasoningID + `","encrypted_content":"gAAAA-encrypted","summary":[]},` +
		`{"type":"function_call","id":"` + longCallID + `","call_id":"call-1","name":"lookup","arguments":"{}"}` +
		`]}`)

	got := SanitizeCodexInputItemIDs(body)
	input := gjson.GetBytes(got, "input").Array()

	if len(input) != 3 {
		t.Fatalf("input length = %d, want 3: %s", len(input), got)
	}
	if gotID := input[0].Get("id").String(); gotID != "msg-1" {
		t.Fatalf("input.0.id = %q, want msg-1", gotID)
	}
	if gotID := input[1].Get("id").String(); gotID != shortReasoningID {
		t.Fatalf("short encrypted reasoning id changed: %q", gotID)
	}
	if gotID := input[2].Get("id").String(); gotID == longCallID || len([]rune(gotID)) != 64 {
		t.Fatalf("ordinary overlong id was not shortened: %q", gotID)
	}
}

func TestSanitizeCodexInputItemIDsShortensOverlongReasoningWithoutEncryptedContent(t *testing.T) {
	longReasoningID := "rs_" + strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name             string
		encryptedContent string
	}{
		{name: "missing"},
		{name: "empty", encryptedContent: `,"encrypted_content":""`},
		{name: "null", encryptedContent: `,"encrypted_content":null`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := []byte(`{"input":[{"type":"reasoning","id":"` + longReasoningID + `"` + testCase.encryptedContent + `,"summary":[]}]}`)

			got := SanitizeCodexInputItemIDs(body)
			input := gjson.GetBytes(got, "input").Array()
			if len(input) != 1 {
				t.Fatalf("input length = %d, want 1: %s", len(input), got)
			}
			gotID := input[0].Get("id").String()
			if gotID == longReasoningID || len([]rune(gotID)) != 64 {
				t.Fatalf("overlong reasoning id was not shortened: %q", gotID)
			}
		})
	}
}

func TestSanitizeCodexInputItemIDsAvoidsExistingIDCollision(t *testing.T) {
	longID := strings.Repeat("grok-item-", 10)
	collidingValidID := shortenCodexInputItemID(longID)
	body := []byte(`{"input":[{"id":"` + longID + `"},{"id":"` + collidingValidID + `"}]}`)

	first := SanitizeCodexInputItemIDs(body)
	second := SanitizeCodexInputItemIDs(body)

	shortened := gjson.GetBytes(first, "input.0.id").String()
	if shortened == collidingValidID {
		t.Fatalf("shortened ID collided with an existing valid ID: %q", shortened)
	}
	if len([]rune(shortened)) > 64 {
		t.Fatalf("shortened ID length = %d, want at most 64", len([]rune(shortened)))
	}
	if actual := gjson.GetBytes(first, "input.1.id").String(); actual != collidingValidID {
		t.Fatalf("existing valid ID changed: %q", actual)
	}
	if actual := gjson.GetBytes(second, "input.0.id").String(); actual != shortened {
		t.Fatalf("collision resolution is not deterministic: first=%q second=%q", shortened, actual)
	}
}

func TestSanitizeCodexInputItemIDsLeavesUnsupportedPayloadsUnchanged(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`not-json`),
		[]byte(`{"input":{"id":"item-1"}}`),
		[]byte(`{"input":[1,{"id":2},{"id":"item-1"}]}`),
	} {
		if got := string(SanitizeCodexInputItemIDs(body)); got != string(body) {
			t.Fatalf("payload changed: got=%q want=%q", got, body)
		}
	}
}
