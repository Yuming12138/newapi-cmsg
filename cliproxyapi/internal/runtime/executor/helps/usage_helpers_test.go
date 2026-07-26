package helps

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestParseOpenAIUsageChatCompletions(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":5}}}`)
	detail := ParseOpenAIUsage(data)
	if detail.InputTokens != 10 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 10)
	}
	if detail.OutputTokens != 6 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 6)
	}
	if detail.TotalTokens != 16 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 16)
	}
	if detail.CachedTokens != 4 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 4)
	}
	if detail.CacheReadTokens != 4 {
		t.Fatalf("cache read tokens = %d, want %d", detail.CacheReadTokens, 4)
	}
	if detail.ReasoningTokens != 5 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 5)
	}
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Quality != usage.TokenAccountingQualityComplete {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
	if detail.TokenBreakdown.Input.UncachedTokens != 6 || detail.TokenBreakdown.Output.NonReasoningTokens != 1 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestParseOpenAIUsageResponses(t *testing.T) {
	data := []byte(`{"service_tier":"default","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":9}}}`)
	detail := ParseOpenAIUsage(data)
	if detail.InputTokens != 10 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 10)
	}
	if detail.OutputTokens != 20 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 20)
	}
	if detail.TotalTokens != 30 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 30)
	}
	if detail.CachedTokens != 7 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 7)
	}
	if detail.CacheReadTokens != 7 {
		t.Fatalf("cache read tokens = %d, want %d", detail.CacheReadTokens, 7)
	}
	if detail.ReasoningTokens != 9 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 9)
	}
	if detail.ResponseServiceTier != "default" {
		t.Fatalf("response service tier = %q, want default", detail.ResponseServiceTier)
	}
	if detail.TokenBreakdown.Input.UncachedTokens != 3 || detail.TokenBreakdown.Output.NonReasoningTokens != 11 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestParseOpenAIUsageTotalOnlyIsUnclassified(t *testing.T) {
	detail := ParseOpenAIUsage([]byte(`{"usage":{"total_tokens":42}}`))
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Quality != usage.TokenAccountingQualityUnclassified ||
		detail.TotalTokens != 42 || detail.TokenBreakdown.UnclassifiedTokens != 42 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseOpenAIUsagePartialBucketsPreserveKnownTokens(t *testing.T) {
	detail := ParseOpenAIUsage([]byte(`{"usage":{"input_tokens":10,"total_tokens":15}}`))
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Quality != usage.TokenAccountingQualityUnclassified ||
		detail.TokenBreakdown.Input.TotalTokens != 10 || detail.TokenBreakdown.UnclassifiedTokens != 5 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseOpenAIUsageExplicitZeroBucketsRemainInconsistent(t *testing.T) {
	detail := ParseOpenAIUsage([]byte(`{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":42}}`))
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Quality != usage.TokenAccountingQualityInconsistent {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseCodexUsageIncludesCacheWriteTokens(t *testing.T) {
	detail, ok := ParseCodexUsage([]byte(`{"response":{"service_tier":"priority","usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":40}}}}`))
	if !ok {
		t.Fatal("ParseCodexUsage() ok = false, want true")
	}
	if detail.CacheReadTokens != 30 || detail.CacheCreationTokens != 40 || detail.TotalTokens != 120 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.TokenBreakdown.Input.UncachedTokens != 30 || detail.TokenBreakdown.Input.CacheWriteTokens != 40 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestParseOpenAIUsageIgnoresNullUsage(t *testing.T) {
	data := []byte(`{"usage":null}`)
	detail := ParseOpenAIUsage(data)
	if detail != (usage.Detail{}) {
		t.Fatalf("detail = %+v, want zero detail", detail)
	}
}

func TestParseOpenAIUsagePreservesResponseTierWithoutUsage(t *testing.T) {
	t.Parallel()

	detail := ParseOpenAIUsage([]byte(`{"service_tier":"default"}`))
	if detail.ResponseServiceTier != "default" {
		t.Fatalf("response service tier = %q, want default", detail.ResponseServiceTier)
	}
}

func TestParseCodexUsagePreservesResponseTierWithoutUsage(t *testing.T) {
	t.Parallel()

	detail, ok := ParseCodexUsage([]byte(`{"response":{"service_tier":"default"}}`))
	if !ok || detail.ResponseServiceTier != "default" {
		t.Fatalf("ParseCodexUsage() = (%+v, %v), want response tier default", detail, ok)
	}
}

func TestParseOpenAIStreamUsageIgnoresNullUsage(t *testing.T) {
	line := []byte(`data: {"id":"chunk_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}],"usage":null}`)
	if detail, ok := ParseOpenAIStreamUsage(line); ok {
		t.Fatalf("ParseOpenAIStreamUsage() = (%+v, true), want false for null usage", detail)
	}
}

func TestParseOpenAIStreamUsageResponsesFields(t *testing.T) {
	line := []byte(`data: {"id":"chunk_1","object":"chat.completion.chunk","service_tier":"flex","choices":[],"usage":{"input_tokens":8,"output_tokens":5,"total_tokens":13,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`)
	detail, ok := ParseOpenAIStreamUsage(line)
	if !ok {
		t.Fatal("ParseOpenAIStreamUsage() ok = false, want true")
	}
	if detail.InputTokens != 8 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 8)
	}
	if detail.OutputTokens != 5 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 5)
	}
	if detail.TotalTokens != 13 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 13)
	}
	if detail.CachedTokens != 3 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 3)
	}
	if detail.ReasoningTokens != 2 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 2)
	}
	if detail.ResponseServiceTier != "flex" {
		t.Fatalf("response service tier = %q, want flex", detail.ResponseServiceTier)
	}
}

func TestStreamUsageBufferPreservesTierAcrossChunks(t *testing.T) {
	t.Parallel()

	var buffer StreamUsageBuffer
	buffer.ObserveOpenAIStream([]byte(`data: {"service_tier":"default"}`))
	buffer.ObserveOpenAIStream([]byte(`data: {"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	detail, ok := buffer.Detail()
	if !ok {
		t.Fatal("Detail() ok = false, want true")
	}
	if detail.InputTokens != 1 || detail.OutputTokens != 1 || detail.ResponseServiceTier != "default" {
		t.Fatalf("detail = %+v, want usage with response tier default", detail)
	}
}

func TestStreamUsageBufferObserveOpenAIStreamStateTransitions(t *testing.T) {
	t.Parallel()

	t.Run("same chunk", func(t *testing.T) {
		var buffer StreamUsageBuffer
		buffer.ObserveOpenAIStream([]byte(`data: {"service_tier":"flex","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
		detail, ok := buffer.Detail()
		if !ok || detail.InputTokens != 2 || detail.ResponseServiceTier != "flex" {
			t.Fatalf("detail = %+v ok=%v", detail, ok)
		}
	})

	t.Run("usage before tier", func(t *testing.T) {
		var buffer StreamUsageBuffer
		buffer.ObserveOpenAIStream([]byte(`data: {"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
		buffer.ObserveOpenAIStream([]byte(`data: {"service_tier":"default"}`))
		detail, ok := buffer.Detail()
		if !ok || detail.InputTokens != 2 || detail.ResponseServiceTier != "default" {
			t.Fatalf("detail = %+v ok=%v", detail, ok)
		}
	})

	t.Run("final usage tier overrides early tier", func(t *testing.T) {
		var buffer StreamUsageBuffer
		buffer.ObserveOpenAIStream([]byte(`data: {"service_tier":"default"}`))
		buffer.ObserveOpenAIStream([]byte(`data: {"service_tier":"priority","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`))
		detail, ok := buffer.Detail()
		if !ok || detail.ResponseServiceTier != "priority" {
			t.Fatalf("detail = %+v ok=%v", detail, ok)
		}
	})

	t.Run("irrelevant and invalid chunks do not change state", func(t *testing.T) {
		var buffer StreamUsageBuffer
		buffer.ObserveOpenAIStream([]byte(`data: {"choices":[{"delta":{"content":"hello"}}]}`))
		buffer.ObserveOpenAIStream([]byte(`data: {"content":"the word \"usage\" appears here"}`))
		buffer.ObserveOpenAIStream([]byte(`data: {"usage":`))
		buffer.ObserveOpenAIStream([]byte(`data: {"usage":null}`))
		if detail, ok := buffer.Detail(); ok {
			t.Fatalf("detail = %+v ok=true, want empty buffer", detail)
		}
	})

	t.Run("zero token usage is retained", func(t *testing.T) {
		var buffer StreamUsageBuffer
		buffer.ObserveOpenAIStream([]byte(`data: {"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
		if _, ok := buffer.Detail(); !ok {
			t.Fatal("Detail() ok = false, want true")
		}
	})
}

func TestParseClaudeUsageIncludesCacheTokensInTotal(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":3085,"output_tokens":253,"cache_read_input_tokens":7,"cache_creation_input_tokens":19514}}`)
	detail := ParseClaudeUsage(data)
	if detail.InputTokens != 3085 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 3085)
	}
	if detail.OutputTokens != 253 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 253)
	}
	if detail.CacheReadTokens != 7 {
		t.Fatalf("cache read tokens = %d, want %d", detail.CacheReadTokens, 7)
	}
	if detail.CacheCreationTokens != 19514 {
		t.Fatalf("cache creation tokens = %d, want %d", detail.CacheCreationTokens, 19514)
	}
	if detail.CachedTokens != 7 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 7)
	}
	if detail.TotalTokens != 22859 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 22859)
	}
}

func TestParseClaudeUsageFallsBackCachedTokensToCacheCreation(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":3085,"output_tokens":253,"cache_creation_input_tokens":19514}}`)
	detail := ParseClaudeUsage(data)
	if detail.CachedTokens != 19514 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 19514)
	}
	if detail.TotalTokens != 22852 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 22852)
	}
}

func TestParseGeminiUsageNormalizesCachedContent(t *testing.T) {
	detail := ParseGeminiUsage([]byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"cachedContentTokenCount":4,"totalTokenCount":12}}`))
	if detail.CacheReadTokens != 4 || detail.TokenBreakdown.Input.UncachedTokens != 6 || detail.TokenBreakdown.TotalTokens != 12 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseGeminiUsageIncludesToolUsePromptTokens(t *testing.T) {
	detail := ParseGeminiUsage([]byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":5,"totalTokenCount":20}}`))
	if detail.InputTokens != 15 || detail.TotalTokens != 20 {
		t.Fatalf("detail = %+v", detail)
	}
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Quality != usage.TokenAccountingQualityComplete ||
		detail.TokenBreakdown.Output.ReasoningTokens != 3 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestParseGeminiUsageRejectsInvalidToolUseSums(t *testing.T) {
	tests := map[string]string{
		"negative": `{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":-1,"totalTokenCount":10}}`,
		"overflow": `{"usageMetadata":{"promptTokenCount":9223372036854775807,"toolUsePromptTokenCount":1,"totalTokenCount":9223372036854775807}}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			detail := ParseGeminiUsage([]byte(payload))
			if detail.InputTokens < 0 || !detail.TokenBreakdown.Valid() ||
				detail.TokenBreakdown.Quality != usage.TokenAccountingQualityInconsistent {
				t.Fatalf("detail = %+v", detail)
			}
		})
	}
}

func TestNormalizeUsageDetailTotalDoesNotDoubleCountReasoning(t *testing.T) {
	detail := normalizeUsageDetailTotal(usage.Detail{
		InputTokens:     100,
		OutputTokens:    30,
		ReasoningTokens: 12,
	}, "openai", "")
	if detail.TotalTokens != 130 {
		t.Fatalf("total tokens = %d, want 130", detail.TotalTokens)
	}
	if detail.TokenBreakdown.Quality != usage.TokenAccountingQualityComplete || detail.TokenBreakdown.Output.ReasoningTokens != 12 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestUsageReporterBuildRecordIncludesLatency(t *testing.T) {
	reporter := &UsageReporter{
		provider:    "openai",
		model:       "gpt-5.4",
		requestedAt: time.Now().Add(-1500 * time.Millisecond),
	}

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.Latency < time.Second {
		t.Fatalf("latency = %v, want >= 1s", record.Latency)
	}
	if record.Latency > 3*time.Second {
		t.Fatalf("latency = %v, want <= 3s", record.Latency)
	}
}

func TestUsageReporterTrackHTTPClientStartsTTFTBeforeRoundTrip(t *testing.T) {
	delay := 40 * time.Millisecond
	reporter := NewUsageReporter(context.Background(), "openai", "gpt-5.4", nil)
	client := reporter.TrackHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			time.Sleep(delay)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		}),
	})

	req, errNewRequest := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/v1/chat/completions", strings.NewReader("{}"))
	if errNewRequest != nil {
		t.Fatalf("NewRequestWithContext() error = %v", errNewRequest)
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		t.Fatalf("Do() error = %v", errDo)
	}
	if _, errRead := io.ReadAll(resp.Body); errRead != nil {
		t.Fatalf("ReadAll() error = %v", errRead)
	}
	if errClose := resp.Body.Close(); errClose != nil {
		t.Fatalf("response body close error = %v", errClose)
	}
	if got := reporter.ttftDuration(); got < delay {
		t.Fatalf("ttft = %v, want >= %v", got, delay)
	}
}

func TestUsageReporterBuildRecordIncludesRequestedModelAlias(t *testing.T) {
	ctx := usage.WithRequestedModelAlias(context.Background(), "client-gpt")
	reporter := NewUsageReporter(ctx, "openai", "gpt-5.4", nil)

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want %q", record.Model, "gpt-5.4")
	}
	if record.Alias != "client-gpt" {
		t.Fatalf("alias = %q, want %q", record.Alias, "client-gpt")
	}
}

func TestNewExecutorUsageReporterIncludesExecutorType(t *testing.T) {
	reporter := NewExecutorUsageReporter(context.Background(), &TestUsageExecutor{}, "gpt-5.4", nil)

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.Provider != "test-provider" {
		t.Fatalf("provider = %q, want %q", record.Provider, "test-provider")
	}
	if record.ExecutorType != "TestUsageExecutor" {
		t.Fatalf("executor type = %q, want %q", record.ExecutorType, "TestUsageExecutor")
	}
}

func TestUsageReporterBuildRecordIncludesReasoningEffort(t *testing.T) {
	ctx := usage.WithReasoningEffort(context.Background(), "medium")
	reporter := NewUsageReporter(ctx, "openai", "gpt-5.4", nil)

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q, want %q", record.ReasoningEffort, "medium")
	}
}

func TestUsageReporterBuildRecordIncludesServiceTier(t *testing.T) {
	ctx := usage.WithServiceTier(context.Background(), "priority")
	reporter := NewUsageReporter(ctx, "openai", "gpt-5.4", nil)

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3, ResponseServiceTier: "default"}, false)
	if record.ServiceTier != "priority" {
		t.Fatalf("service tier = %q, want %q", record.ServiceTier, "priority")
	}
	if record.RequestServiceTier != "priority" {
		t.Fatalf("request service tier = %q, want priority", record.RequestServiceTier)
	}
	if record.ResponseServiceTier != "default" {
		t.Fatalf("response service tier = %q, want default", record.ResponseServiceTier)
	}
}

func TestRequestServiceTierForRecordFallback(t *testing.T) {
	tests := []struct {
		name   string
		record usage.Record
		want   string
	}{
		{
			name:   "explicit request tier",
			record: usage.Record{ServiceTier: "legacy", RequestServiceTier: "priority"},
			want:   "priority",
		},
		{
			name:   "legacy service tier",
			record: usage.Record{ServiceTier: "flex"},
			want:   "flex",
		},
		{
			name:   "default",
			record: usage.Record{},
			want:   usage.DefaultServiceTier,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requestServiceTierForRecord(test.record); got != test.want {
				t.Fatalf("requestServiceTierForRecord() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUsageReporterSetTranslatedReasoningEffortUpdatesServiceTier(t *testing.T) {
	reporter := NewUsageReporter(context.Background(), "openai", "gpt-5.4", nil)

	reporter.SetTranslatedReasoningEffort([]byte(`{"service_tier":"priority"}`), "openai")

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.ServiceTier != "priority" {
		t.Fatalf("service tier = %q, want %q", record.ServiceTier, "priority")
	}
}

func TestUsageReporterSetTranslatedReasoningEffortDefaultsServiceTierWhenRemoved(t *testing.T) {
	ctx := usage.WithServiceTier(context.Background(), "priority")
	reporter := NewUsageReporter(ctx, "openai", "gpt-5.4", nil)

	reporter.SetTranslatedReasoningEffort([]byte(`{"model":"gpt-5.4"}`), "openai")

	record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
	if record.ServiceTier != usage.DefaultServiceTier {
		t.Fatalf("service tier = %q, want %q", record.ServiceTier, usage.DefaultServiceTier)
	}
}

func TestUsageReporterBuildAdditionalModelRecordSkipsZeroTokens(t *testing.T) {
	reporter := &UsageReporter{
		provider:    "codex",
		model:       "gpt-5.4",
		requestedAt: time.Now(),
	}

	if _, ok := reporter.buildAdditionalModelRecord("gpt-image-2", usage.Detail{}); ok {
		t.Fatalf("expected all-zero token usage to be skipped")
	}
	if _, ok := reporter.buildAdditionalModelRecord("gpt-image-2", usage.Detail{InputTokens: 2}); !ok {
		t.Fatalf("expected non-zero input token usage to be recorded")
	}
	if _, ok := reporter.buildAdditionalModelRecord("gpt-image-2", usage.Detail{CachedTokens: 2}); !ok {
		t.Fatalf("expected non-zero cached token usage to be recorded")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type TestUsageExecutor struct{}

func (TestUsageExecutor) Identifier() string {
	return "test-provider"
}
