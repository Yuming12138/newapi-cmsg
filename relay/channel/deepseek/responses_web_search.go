package deepseek

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	deepSeekDoWebSearchToolName   = "do_web_search"
	deepSeekWebSearchMaxRounds    = 5
	deepSeekWebSearchMaxTotalRuns = 12
	deepSeekWebSearchLimitMessage = "已经多次搜索了，是否继续？如果确实必须继续搜索，请再次调用 do_web_search；否则请基于已有搜索结果回答用户。"
)

type internalWebSearchCall struct {
	ID    string
	Query string
}

type internalWebSearchResult struct {
	Output            string
	Usage             dto.Usage
	WebSearchRequests int
}

func hasInternalWebSearchTool(toolMap map[string]toolRestore) bool {
	restore, ok := toolMap[deepSeekDoWebSearchToolName]
	return ok && restore.Type == "internal_web_search"
}

func isActiveResponsesWebSearchTool(tool map[string]any) bool {
	if external, ok := tool["external_web_access"].(bool); ok && !external {
		return false
	}
	return true
}

func deepSeekDoWebSearchTool(tool map[string]any) map[string]any {
	parameters := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Focused web search query.",
			},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
	defaults := make(map[string]any)
	for _, key := range []string{"max_uses", "user_location"} {
		if value, ok := tool[key]; ok {
			defaults[key] = value
		}
	}
	if len(defaults) > 0 {
		parameters["x-deepseek-web-search-defaults"] = defaults
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        deepSeekDoWebSearchToolName,
			"description": "Search the live web when current or external information is needed. The gateway will execute DeepSeek web search and return text or null.",
			"parameters":  parameters,
		},
	}
}

func runDeepSeekInternalWebSearchLoop(c *gin.Context, info *relaycommon.RelayInfo, state *responsesTurnState, first *dto.OpenAITextResponse) (dto.OpenAITextResponse, *types.NewAPIError) {
	result := *first
	payload := cloneChatPayload(state.ChatPayload)
	totalUsage := dto.Usage{}
	addUsage(&totalUsage, result.Usage)
	searchRoundsSinceQuestion := 0
	totalSearchRuns := 0

	for {
		calls := findInternalWebSearchCalls(parseChatToolCalls(firstChoiceMessage(&result).ToolCalls))
		if len(calls) == 0 {
			break
		}

		if searchRoundsSinceQuestion >= deepSeekWebSearchMaxRounds {
			appendInternalToolExchange(payload, firstChoiceMessage(&result), calls, func(call internalWebSearchCall) string {
				return deepSeekWebSearchLimitMessage
			})
			next, err := postDeepSeekChatCompletion(c, info, payload)
			if err != nil {
				return result, err
			}
			addUsage(&totalUsage, next.Usage)
			result = *next
			searchRoundsSinceQuestion = 0
			continue
		}

		appendInternalToolExchange(payload, firstChoiceMessage(&result), calls, func(call internalWebSearchCall) string {
			if totalSearchRuns >= deepSeekWebSearchMaxTotalRuns {
				return "null"
			}
			totalSearchRuns++
			searchResult := runDeepSeekWebSearchSafely(c, info, call.Query)
			addUsage(&totalUsage, searchResult.Usage)
			if searchResult.WebSearchRequests > 0 {
				c.Set("claude_web_search_requests", c.GetInt("claude_web_search_requests")+searchResult.WebSearchRequests)
			}
			if strings.TrimSpace(searchResult.Output) == "" {
				return "null"
			}
			return searchResult.Output
		})

		next, err := postDeepSeekChatCompletion(c, info, payload)
		if err != nil {
			return result, err
		}
		addUsage(&totalUsage, next.Usage)
		result = *next
		searchRoundsSinceQuestion++
		if totalSearchRuns >= deepSeekWebSearchMaxTotalRuns {
			break
		}
	}

	normalizeCombinedUsage(&totalUsage)
	result.Usage = totalUsage
	return result, nil
}

func cloneChatPayload(payload map[string]any) map[string]any {
	cloned := copyMap(payload)
	if messages, ok := payload["messages"].([]dto.Message); ok {
		copiedMessages := make([]dto.Message, len(messages))
		copy(copiedMessages, messages)
		cloned["messages"] = copiedMessages
	}
	return cloned
}

func firstChoiceMessage(response *dto.OpenAITextResponse) dto.Message {
	if response == nil || len(response.Choices) == 0 {
		return dto.Message{Role: "assistant"}
	}
	return response.Choices[0].Message
}

func findInternalWebSearchCalls(toolCalls []chatToolCall) []internalWebSearchCall {
	calls := make([]internalWebSearchCall, 0)
	for _, toolCall := range toolCalls {
		if toolCall.Function.Name != deepSeekDoWebSearchToolName {
			continue
		}
		query := parseInternalWebSearchArguments(toolCall.Function.Arguments)
		if query == "" {
			query = "current information"
		}
		callID := toolCall.ID
		if callID == "" {
			callID = "call_" + common.GetUUID()
		}
		calls = append(calls, internalWebSearchCall{ID: callID, Query: query})
	}
	return calls
}

func parseInternalWebSearchArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var parsed map[string]any
	if err := common.Unmarshal(common.StringToByteSlice(arguments), &parsed); err != nil {
		return arguments
	}
	for _, key := range []string{"query", "input", "q"} {
		if query := strings.TrimSpace(common.Interface2String(parsed[key])); query != "" {
			return query
		}
	}
	return ""
}

func appendInternalToolExchange(payload map[string]any, assistantMessage dto.Message, calls []internalWebSearchCall, output func(internalWebSearchCall) string) {
	messages := chatPayloadMessages(payload)
	assistant := dto.Message{Role: "assistant", Content: nil}
	internalToolCalls := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		internalToolCalls = append(internalToolCalls, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      deepSeekDoWebSearchToolName,
				"arguments": searchQueryArguments(call.Query),
			},
		})
	}
	if len(internalToolCalls) > 0 {
		assistant.SetToolCalls(internalToolCalls)
	} else {
		assistant.ToolCalls = assistantMessage.ToolCalls
	}
	messages = append(messages, assistant)
	for _, call := range calls {
		messages = append(messages, dto.Message{
			Role:       "tool",
			ToolCallId: call.ID,
			Content:    output(call),
		})
	}
	payload["messages"] = messages
}

func searchQueryArguments(query string) string {
	data, err := common.Marshal(map[string]string{"query": query})
	if err != nil {
		return `{}`
	}
	return string(data)
}

func chatPayloadMessages(payload map[string]any) []dto.Message {
	if messages, ok := payload["messages"].([]dto.Message); ok {
		return messages
	}
	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		return nil
	}
	messages := make([]dto.Message, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		if message, ok := rawMessage.(dto.Message); ok {
			messages = append(messages, message)
			continue
		}
		data, err := common.Marshal(rawMessage)
		if err != nil {
			continue
		}
		var message dto.Message
		if err := common.Unmarshal(data, &message); err == nil {
			messages = append(messages, message)
		}
	}
	return messages
}

func runDeepSeekWebSearchSafely(c *gin.Context, info *relaycommon.RelayInfo, query string) internalWebSearchResult {
	result, err := runDeepSeekWebSearch(c, info, query)
	if err != nil {
		return internalWebSearchResult{Output: fmt.Sprintf(`{"error":%q}`, err.Error())}
	}
	return result
}

func runDeepSeekWebSearch(c *gin.Context, info *relaycommon.RelayInfo, query string) (internalWebSearchResult, error) {
	maxTokens := uint(1024)
	payload := map[string]any{
		"model":      info.UpstreamModelName,
		"max_tokens": maxTokens,
		"messages": []map[string]any{{
			"role":    "user",
			"content": "Search the web for the following query and return concise findings with source titles and URLs when available: " + query,
		}},
		"tools": []map[string]any{{
			"type":     "web_search_20250305",
			"name":     "web_search",
			"max_uses": 1,
		}},
	}
	body, statusCode, err := postDeepSeekJSON(c, info, deepSeekAnthropicMessagesURL(info.ChannelBaseUrl), payload, true)
	if err != nil {
		return internalWebSearchResult{}, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return internalWebSearchResult{}, fmt.Errorf("deepseek web search status=%d body=%s", statusCode, truncateForToolOutput(string(body), 800))
	}
	var claudeResponse dto.ClaudeResponse
	if err := common.Unmarshal(body, &claudeResponse); err != nil {
		return internalWebSearchResult{}, err
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return internalWebSearchResult{}, fmt.Errorf("%s", claudeError.Message)
	}
	usage := usageFromClaudeUsage(claudeResponse.Usage)
	webSearchRequests := 0
	if claudeResponse.Usage != nil && claudeResponse.Usage.ServerToolUse != nil {
		webSearchRequests = claudeResponse.Usage.ServerToolUse.WebSearchRequests
	}
	return internalWebSearchResult{
		Output:            extractClaudeSearchOutput(&claudeResponse),
		Usage:             usage,
		WebSearchRequests: webSearchRequests,
	}, nil
}

func extractClaudeSearchOutput(response *dto.ClaudeResponse) string {
	if response == nil {
		return ""
	}
	textParts := make([]string, 0)
	searchResults := make([]any, 0)
	for _, item := range response.Content {
		switch item.Type {
		case "text":
			if text := strings.TrimSpace(item.GetText()); text != "" {
				textParts = append(textParts, text)
			}
		case "web_search_tool_result":
			if item.Content != nil {
				searchResults = append(searchResults, item.Content)
			}
		}
	}
	var builder strings.Builder
	if len(textParts) > 0 {
		builder.WriteString(strings.Join(textParts, "\n"))
	}
	if len(searchResults) > 0 {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		data, err := common.Marshal(searchResults)
		if err == nil {
			builder.WriteString("web_search_results: ")
			builder.WriteString(truncateForToolOutput(string(data), 6000))
		}
	}
	return builder.String()
}

func postDeepSeekChatCompletion(c *gin.Context, info *relaycommon.RelayInfo, payload map[string]any) (*dto.OpenAITextResponse, *types.NewAPIError) {
	body, statusCode, err := postDeepSeekJSON(c, info, deepSeekChatCompletionsURL(info.ChannelBaseUrl), payload, false)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	var chatResponse dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := chatResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, statusCode)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, types.NewOpenAIError(fmt.Errorf("deepseek chat status=%d body=%s", statusCode, truncateForToolOutput(string(body), 800)), types.ErrorCodeBadResponseStatusCode, statusCode)
	}
	return &chatResponse, nil
}

func postDeepSeekJSON(c *gin.Context, info *relaycommon.RelayInfo, url string, payload any, anthropic bool) ([]byte, int, error) {
	data, err := common.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	if anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	proxyURL := ""
	if info != nil && info.ChannelMeta != nil {
		proxyURL = strings.TrimSpace(info.ChannelSetting.Proxy)
	}
	client, err := service.GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return nil, 0, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func deepSeekRootBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/beta")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return baseURL
}

func deepSeekChatCompletionsURL(baseURL string) string {
	return deepSeekRootBaseURL(baseURL) + "/v1/chat/completions"
}

func deepSeekAnthropicMessagesURL(baseURL string) string {
	return deepSeekRootBaseURL(baseURL) + "/anthropic/v1/messages"
}

func usageFromClaudeUsage(claudeUsage *dto.ClaudeUsage) dto.Usage {
	if claudeUsage == nil {
		return dto.Usage{}
	}
	usage := dto.Usage{
		PromptTokens:     claudeUsage.InputTokens,
		CompletionTokens: claudeUsage.OutputTokens,
		InputTokens:      claudeUsage.InputTokens,
		OutputTokens:     claudeUsage.OutputTokens,
		UsageSource:      "deepseek_anthropic_web_search",
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         claudeUsage.CacheReadInputTokens,
			CachedCreationTokens: claudeUsage.CacheCreationInputTokens,
		},
		ClaudeCacheCreation5mTokens: claudeUsage.GetCacheCreation5mTokens(),
		ClaudeCacheCreation1hTokens: claudeUsage.GetCacheCreation1hTokens(),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func addUsage(total *dto.Usage, usage dto.Usage) {
	if total == nil {
		return
	}
	cacheReadTokens := usage.PromptTokensDetails.CachedTokens
	if cacheReadTokens < 0 {
		cacheReadTokens = 0
	}
	cacheCreationTokens := usage.PromptTokensDetails.CacheCreationTokensTotal()
	cacheExclusionTokens := usage.CacheReadWriteExclusionTokens
	if cacheExclusionTokens <= 0 {
		cacheExclusionTokens = cacheReadTokens + cacheCreationTokens
		if usage.HasNativeOpenAICacheWriteTokens() && cacheReadTokens > cacheCreationTokens {
			cacheExclusionTokens = cacheReadTokens
		} else if usage.HasNativeOpenAICacheWriteTokens() {
			cacheExclusionTokens = cacheCreationTokens
		}
	}
	promptTokens := usage.PromptTokens
	if usage.UsageSemantic == "anthropic" && usage.CacheReadWriteExclusionTokens <= 0 {
		promptTokens += cacheReadTokens + cacheCreationTokens
	}
	if promptTokens < 0 {
		promptTokens = 0
	}
	completionTokens := usage.CompletionTokens
	if completionTokens < 0 {
		completionTokens = 0
	}

	total.PromptTokens += promptTokens
	total.CompletionTokens += completionTokens
	total.TotalTokens += promptTokens + completionTokens
	total.InputTokens += promptTokens
	total.OutputTokens += completionTokens
	total.PromptCacheHitTokens += usage.PromptCacheHitTokens
	total.CacheReadWriteExclusionTokens += cacheExclusionTokens
	total.PromptTokensDetails.CachedTokens += cacheReadTokens
	// Normalize aliases per request before accumulation. Taking max only after
	// summing each alias separately undercounts mixed native/legacy rounds.
	total.PromptTokensDetails.CachedCreationTokens += usage.PromptTokensDetails.CacheCreationTokensTotal()
	if usage.PromptTokensDetails.CacheWriteTokens > 0 {
		total.PromptTokensDetails.CacheWriteTokens += usage.PromptTokensDetails.CacheWriteTokens
	}
	total.PromptTokensDetails.TextTokens += usage.PromptTokensDetails.TextTokens
	total.PromptTokensDetails.AudioTokens += usage.PromptTokensDetails.AudioTokens
	total.PromptTokensDetails.ImageTokens += usage.PromptTokensDetails.ImageTokens
	total.CompletionTokenDetails.TextTokens += usage.CompletionTokenDetails.TextTokens
	total.CompletionTokenDetails.AudioTokens += usage.CompletionTokenDetails.AudioTokens
	total.CompletionTokenDetails.ImageTokens += usage.CompletionTokenDetails.ImageTokens
	total.CompletionTokenDetails.ReasoningTokens += usage.CompletionTokenDetails.ReasoningTokens
	total.ClaudeCacheCreation5mTokens += usage.ClaudeCacheCreation5mTokens
	total.ClaudeCacheCreation1hTokens += usage.ClaudeCacheCreation1hTokens
}

func normalizeCombinedUsage(usage *dto.Usage) {
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	usage.InputTokens = usage.PromptTokens
	usage.OutputTokens = usage.CompletionTokens
	usage.UsageSource = "deepseek_chat_web_search"
	usage.UsageSemantic = "openai"
}

func truncateForToolOutput(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
