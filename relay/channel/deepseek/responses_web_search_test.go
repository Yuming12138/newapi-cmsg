package deepseek

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesToolsToChatExposesInternalWebSearchTool(t *testing.T) {
	tools, toolMap := responsesToolsToChat([]map[string]any{
		{"type": "web_search", "external_web_access": true},
		{"type": "function", "name": "local_shell", "parameters": map[string]any{"type": "object"}},
	})

	require.Len(t, tools, 2)
	require.Equal(t, deepSeekDoWebSearchToolName, chatFunctionName(tools[0]))
	require.Equal(t, "local_shell", chatFunctionName(tools[1]))
	require.True(t, hasInternalWebSearchTool(toolMap))
}

func TestResponsesToolsToChatSkipsDisabledWebSearchTool(t *testing.T) {
	tools, toolMap := responsesToolsToChat([]map[string]any{
		{"type": "web_search", "external_web_access": false},
	})

	require.Empty(t, tools)
	require.False(t, hasInternalWebSearchTool(toolMap))
}

func TestExtractClaudeSearchOutputKeepsTextAndRawResults(t *testing.T) {
	text := "final searched answer"
	response := &dto.ClaudeResponse{
		Content: []dto.ClaudeMediaMessage{
			{Type: "thinking"},
			{
				Type:    "web_search_tool_result",
				Content: []any{map[string]any{"title": "DeepSeek Docs", "url": "https://api-docs.deepseek.com"}},
			},
			{Type: "text", Text: &text},
		},
	}

	output := extractClaudeSearchOutput(response)

	require.Contains(t, output, "final searched answer")
	require.Contains(t, output, "web_search_results")
	require.Contains(t, output, "https://api-docs.deepseek.com")
}

func TestDeepSeekRootBaseURLTrimsCommonSuffixes(t *testing.T) {
	require.Equal(t, "https://api.deepseek.com", deepSeekRootBaseURL("https://api.deepseek.com/v1"))
	require.Equal(t, "https://api.deepseek.com", deepSeekRootBaseURL("https://api.deepseek.com/beta"))
	require.True(t, strings.HasSuffix(deepSeekChatCompletionsURL("https://api.deepseek.com/v1"), "/v1/chat/completions"))
	require.True(t, strings.HasSuffix(deepSeekAnthropicMessagesURL("https://api.deepseek.com/v1"), "/anthropic/v1/messages"))
}

func TestAddUsageAccumulatesCacheWriteTokens(t *testing.T) {
	total := dto.Usage{}
	addUsage(&total, dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 10,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         200,
			CachedCreationTokens: 100,
			CacheWriteTokens:     300,
		},
	})
	addUsage(&total, dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
	})
	normalizeCombinedUsage(&total)

	require.Equal(t, 1180, total.PromptTokens)
	require.Equal(t, 30, total.CompletionTokens)
	require.Equal(t, 1210, total.TotalTokens)
	require.Equal(t, 1180, total.InputTokens)
	require.Equal(t, 30, total.OutputTokens)
	require.Equal(t, 230, total.PromptTokensDetails.CachedTokens)
	require.Equal(t, 350, total.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 300, total.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 350, total.PromptTokensDetails.CacheCreationTokensTotal())
	require.Equal(t, 380, total.CacheReadWriteExclusionTokens)
	require.Equal(t, "openai", total.UsageSemantic)
	require.Equal(t, "deepseek_chat_web_search", total.UsageSource)
}

func TestUsageFromClaudeUsageMarksTextOnlyAnthropicSemantics(t *testing.T) {
	usage := usageFromClaudeUsage(&dto.ClaudeUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 50,
	})

	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	require.Equal(t, 30, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 50, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
}

func TestRunDeepSeekInternalWebSearchLoopExecutesSearchAndContinues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mux := http.NewServeMux()
	mux.HandleFunc("/anthropic/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "message",
			"content": []map[string]any{
				{
					"type": "web_search_tool_result",
					"content": []map[string]any{
						{"title": "Weather", "url": "https://example.com/weather"},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  7,
				"output_tokens": 11,
				"server_tool_use": map[string]any{
					"web_search_requests": 1,
				},
			},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		messages := payload["messages"].([]any)
		require.Len(t, messages, 3)
		require.Equal(t, "tool", messages[2].(map[string]any)["role"])
		require.Contains(t, messages[2].(map[string]any)["content"], "https://example.com/weather")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "final with search",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     13,
				"completion_tokens": 17,
				"total_tokens":      30,
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName:         "deepseek-v4-pro[1m]",
		ChannelMeta:             &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "test-key", UpstreamModelName: "deepseek-v4-pro"},
		FinalRequestRelayFormat: "",
	}
	state := &responsesTurnState{
		Request:           dto.OpenAIResponsesRequest{Model: "deepseek-v4-pro[1m]"},
		ChatPayload:       map[string]any{"model": "deepseek-v4-pro", "messages": []dto.Message{{Role: "user", Content: "weather"}}, "stream": false},
		InternalWebSearch: true,
	}
	first := &dto.OpenAITextResponse{
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message: dto.Message{
					Role:      "assistant",
					Content:   nil,
					ToolCalls: json.RawMessage(`[{"id":"call_search","type":"function","function":{"name":"do_web_search","arguments":"{\"query\":\"weather\"}"}}]`),
				},
			},
		},
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, InputTokens: 3, OutputTokens: 5},
	}

	final, err := runDeepSeekInternalWebSearchLoop(ctx, info, state, first)

	require.Nil(t, err)
	require.Equal(t, "final with search", final.Choices[0].Message.StringContent())
	require.Equal(t, 23, final.Usage.PromptTokens)
	require.Equal(t, 33, final.Usage.CompletionTokens)
	require.Equal(t, 56, final.Usage.TotalTokens)
	require.Equal(t, 1, ctx.GetInt("claude_web_search_requests"))
}
