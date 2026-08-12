package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

var benchmarkSanitizeOpenAIResponsesReasoningOutput []byte

func TestSanitizeOpenAIResponsesReasoningEncryptedContentStripsOrphanIDsWhenStoreDisabled(t *testing.T) {
	valid := validCodexReasoningEncryptedContentForTest()
	body := []byte(`{"store":false,"input":[` +
		`{"id":"rs_bad","type":"reasoning","encrypted_content":"bad","summary":[]},` +
		`{"id":"rs_orphan","type":"reasoning","summary":[]},` +
		`{"id":"rs_good","type":"reasoning","encrypted_content":"` + valid + `","summary":[]},` +
		`{"id":"msg_1","type":"message","role":"user","content":"hi"}` +
		`]}`)

	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)

	if gjson.GetBytes(got, "input.0.encrypted_content").Exists() {
		t.Fatalf("invalid encrypted_content still present: %s", got)
	}
	if gjson.GetBytes(got, "input.0.id").Exists() {
		t.Fatalf("invalid reasoning id should be stripped when store=false: %s", got)
	}
	if gjson.GetBytes(got, "input.1.id").Exists() {
		t.Fatalf("orphan reasoning id should be stripped when store=false: %s", got)
	}
	if gotID := gjson.GetBytes(got, "input.2.id").String(); gotID != "rs_good" {
		t.Fatalf("valid reasoning id = %q, want rs_good; body=%s", gotID, got)
	}
	if gotID := gjson.GetBytes(got, "input.3.id").String(); gotID != "msg_1" {
		t.Fatalf("non-reasoning id should stay: %s", got)
	}
}

func TestSanitizeOpenAIResponsesReasoningEncryptedContentKeepsIDsWhenStoreEnabled(t *testing.T) {
	body := []byte(`{"store":true,"input":[` +
		`{"id":"rs_bad","type":"reasoning","encrypted_content":"bad","summary":[]},` +
		`{"id":"rs_orphan","type":"reasoning","summary":[]}` +
		`]}`)

	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)

	if gjson.GetBytes(got, "input.0.encrypted_content").Exists() {
		t.Fatalf("invalid encrypted_content still present: %s", got)
	}
	if gotID := gjson.GetBytes(got, "input.0.id").String(); gotID != "rs_bad" {
		t.Fatalf("store=true should keep reasoning id after dropping invalid encrypted_content, got %q body=%s", gotID, got)
	}
	if gotID := gjson.GetBytes(got, "input.1.id").String(); gotID != "rs_orphan" {
		t.Fatalf("store=true should keep orphan reasoning id, got %q body=%s", gotID, got)
	}
}

func TestSanitizeOpenAIResponsesReasoningEncryptedContentNoopReturnsOriginalBody(t *testing.T) {
	valid := validCodexReasoningEncryptedContentForTest()
	body := []byte(`{"store":false,"input":[{"id":"rs_good","type":"reasoning","encrypted_content":"` + valid + `","summary":[]},{"role":"user","content":"hi"}]}`)
	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
	if string(got) != string(body) {
		t.Fatalf("noop path should return original body unchanged\ngot=%s\nwant=%s", got, body)
	}
	if len(got) > 0 && len(body) > 0 && &got[0] != &body[0] {
		t.Fatal("noop path should return the original body slice")
	}
}

func TestSanitizeOpenAIResponsesReasoningEncryptedContentRebuildsInputOnce(t *testing.T) {
	body := []byte(`{"store":false,"input":[` +
		`{"id":"rs_bad_1","type":"reasoning","encrypted_content":"bad","summary":[]},` +
		`{"id":"msg_1","type":"message","role":"assistant","content":"keep"},` +
		`{"id":"rs_bad_2","type":"reasoning","encrypted_content":null,"summary":[]},` +
		`{"id":"msg_2","type":"message","role":"user","content":"next"}` +
		`]}`)
	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
	if len(gjson.GetBytes(got, "input").Array()) != 4 {
		t.Fatalf("input length changed: %s", got)
	}
	if gjson.GetBytes(got, "input.0.id").Exists() || gjson.GetBytes(got, "input.0.encrypted_content").Exists() {
		t.Fatalf("first invalid reasoning item was not sanitized: %s", got)
	}
	if gotText := gjson.GetBytes(got, "input.1.content").String(); gotText != "keep" {
		t.Fatalf("middle message changed: %q", gotText)
	}
	if gjson.GetBytes(got, "input.2.id").Exists() || gjson.GetBytes(got, "input.2.encrypted_content").Exists() {
		t.Fatalf("second invalid reasoning item was not sanitized: %s", got)
	}
	if gotText := gjson.GetBytes(got, "input.3.content").String(); gotText != "next" {
		t.Fatalf("trailing message changed: %q", gotText)
	}
}

func BenchmarkSanitizeOpenAIResponsesReasoningEncryptedContentLargeNoopPayload(b *testing.B) {
	body := []byte(`{"store":false,"input":[{"type":"message","role":"user","content":"` + strings.Repeat("x", 8<<20) + `"}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		benchmarkSanitizeOpenAIResponsesReasoningOutput = sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "benchmark", body)
	}
}
