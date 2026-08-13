package deepseek

import (
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const nativeResponsesToolMapKey = "deepseek_native_responses_tool_map"

var dsmlParameterOpenPattern = regexp.MustCompile(`(?is)<\s*([^<>]*?DSML[^<>]*?)\s*parameter\b([^>]*)>`)
var dsmlNameAttributePattern = regexp.MustCompile(`(?is)\bname\s*=\s*(?:"([^"]*)"|'([^']*)')`)
var dsmlStringAttributePattern = regexp.MustCompile(`(?is)\bstring\s*=\s*(?:"([^"]*)"|'([^']*)')`)

type nativeResponseTool struct {
	Type      string
	Name      string
	Namespace string
}

type dsmlInvocation struct {
	Name       string
	Parameters map[string]any
}

type dsmlConversion struct {
	Text  string
	Tools []dto.ResponsesOutput
}

type nativeResponsesStreamState struct {
	toolMap        map[string]nativeResponseTool
	buffer         strings.Builder
	buffering      bool
	pendingText    string
	nextOutput     int
	completedTools []dto.ResponsesOutput
}

func setNativeResponsesToolMap(c *gin.Context, tools []map[string]any) {
	if c == nil {
		return
	}
	c.Set(nativeResponsesToolMapKey, buildNativeResponsesToolMap(tools))
}

func getNativeResponsesToolMap(c *gin.Context) map[string]nativeResponseTool {
	if c == nil {
		return nil
	}
	value, ok := c.Get(nativeResponsesToolMapKey)
	if !ok {
		return nil
	}
	toolMap, _ := value.(map[string]nativeResponseTool)
	return toolMap
}

func buildNativeResponsesToolMap(tools []map[string]any) map[string]nativeResponseTool {
	toolMap := make(map[string]nativeResponseTool)
	for _, tool := range tools {
		toolType := strings.TrimSpace(common.Interface2String(tool["type"]))
		switch toolType {
		case "function":
			name := responseToolName(tool)
			if name != "" {
				toolMap[name] = nativeResponseTool{Type: "function", Name: name}
			}
		case "namespace":
			namespace := strings.TrimSpace(common.Interface2String(tool["name"]))
			children := mapSlice(tool["tools"])
			for _, child := range children {
				if common.Interface2String(child["type"]) != "function" {
					continue
				}
				name := strings.TrimSpace(common.Interface2String(child["name"]))
				flatName := namespacedToolName(namespace, name)
				if name != "" {
					restore := nativeResponseTool{Type: "function", Name: name, Namespace: namespace}
					toolMap[flatName] = restore
					toolMap[namespace+"__"+name] = restore
				}
			}
		case "web_search", "web_search_preview", "file_search", "image_generation", "computer_use_preview":
			continue
		default:
			name := strings.TrimSpace(common.Interface2String(tool["name"]))
			if name == "" {
				name = toolType
			}
			if name != "" {
				toolMap[name] = nativeResponseTool{Type: "custom", Name: name}
			}
		}
	}
	return toolMap
}

func responseToolName(tool map[string]any) string {
	if function, ok := tool["function"].(map[string]any); ok {
		return strings.TrimSpace(common.Interface2String(function["name"]))
	}
	return strings.TrimSpace(common.Interface2String(tool["name"]))
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func handleNativeResponsesResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if info.IsStream {
		return handleNativeResponsesStream(c, resp, info)
	}
	defer service.CloseResponseBodyGracefully(resp)
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var response dto.OpenAIResponsesResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := response.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}
	converted, changed := convertNativeResponsesOutput(response.Output, getNativeResponsesToolMap(c))
	if changed {
		response.Output = converted
		responseBody, err = common.Marshal(response)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
		}
	}
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return nativeResponsesUsage(&response, info), nil
}

func handleNativeResponsesStream(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	state := &nativeResponsesStreamState{toolMap: getNativeResponsesToolMap(c)}
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var event dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &event); err != nil {
			logger.LogError(c, "failed to unmarshal deepseek responses stream: "+err.Error())
			sr.Error(err)
			return
		}
		if event.OutputIndex != nil && *event.OutputIndex >= state.nextOutput {
			state.nextOutput = *event.OutputIndex + 1
		}
		if event.Type == "response.output_text.delta" {
			consumed, outputText := state.consumeTextDelta(event.Delta)
			if outputText != "" {
				prefixEvent := event
				prefixEvent.Delta = outputText
				if err := writeNativeResponsesSSE(c, prefixEvent.Type, prefixEvent); err != nil {
					sr.Stop(err)
					return
				}
				info.StreamStatus.RecordWrite()
				info.SendResponseCount++
			}
			if consumed {
				return
			}
		}
		if state.buffering && isNativeResponsesTerminalEvent(event.Type) {
			terminalType := event.Type
			if err := state.flushBufferedText(c); err != nil {
				sr.Stop(err)
				return
			}
			if terminalType == "response.output_text.done" && len(state.completedTools) > 0 {
				return
			}
		}
		if event.Type == dto.ResponsesOutputTypeItemDone && event.Item != nil && event.Item.Type == "message" {
			converted, changed := stripDSMLToolsFromNativeOutput([]dto.ResponsesOutput{*event.Item}, state.toolMap)
			if changed {
				if len(converted) == 0 {
					event.Item.Content = nil
				} else {
					event.Item = &converted[0]
				}
				dataBytes, err := common.Marshal(event)
				if err != nil {
					sr.Stop(err)
					return
				}
				data = string(dataBytes)
			}
		}
		if event.Type == "response.completed" && event.Response != nil {
			if event.Response.Usage != nil {
				copyNativeResponsesUsage(usage, event.Response.Usage)
			}
			converted, changed := stripDSMLToolsFromNativeOutput(event.Response.Output, state.toolMap)
			if changed {
				event.Response.Output = converted
			}
			event.Response.Output = mergeNativeResponseTools(event.Response.Output, state.completedTools)
			dataBytes, err := common.Marshal(event)
			if err != nil {
				sr.Stop(err)
				return
			}
			data = string(dataBytes)
		}
		if err := helper.ResponseChunkData(c, event, data); err != nil {
			sr.Stop(err)
			return
		}
		info.StreamStatus.RecordWrite()
		info.SendResponseCount++
	})
	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, nil
}

func (s *nativeResponsesStreamState) consumeTextDelta(delta string) (bool, string) {
	hadPending := s.pendingText != ""
	combined := s.pendingText + delta
	s.pendingText = ""
	if s.buffering {
		s.buffer.WriteString(combined)
		return true, ""
	}
	if index := dsmlStartIndex(combined); index >= 0 {
		s.buffering = true
		s.buffer.WriteString(combined[index:])
		return true, combined[:index]
	}
	if index := strings.LastIndex(combined, "<"); index >= 0 && !strings.Contains(combined[index:], ">") {
		s.pendingText = combined[index:]
		return true, combined[:index]
	}
	if hadPending {
		return true, combined
	}
	return false, ""
}

func (s *nativeResponsesStreamState) flushBufferedText(c *gin.Context) error {
	if !s.buffering {
		if s.pendingText == "" {
			return nil
		}
		pending := s.pendingText
		s.pendingText = ""
		return writeNativeResponsesSSE(c, "response.output_text.delta", dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: pending})
	}
	text := s.buffer.String()
	s.buffer.Reset()
	s.buffering = false
	converted := convertDSMLText(text, s.toolMap)
	if converted.Text != "" {
		if err := writeNativeResponsesSSE(c, "response.output_text.delta", dto.ResponsesStreamResponse{Type: "response.output_text.delta", Delta: converted.Text}); err != nil {
			return err
		}
	}
	for _, tool := range converted.Tools {
		s.completedTools = append(s.completedTools, tool)
		if err := s.writeToolEvents(c, tool); err != nil {
			return err
		}
	}
	return nil
}

func (s *nativeResponsesStreamState) writeToolEvents(c *gin.Context, tool dto.ResponsesOutput) error {
	index := s.nextOutput
	s.nextOutput++
	added := tool
	added.Status = "in_progress"
	if tool.Type == "custom_tool_call" {
		added.Input = rawString("")
	} else {
		added.Arguments = rawString("")
	}
	if err := writeNativeResponsesSSE(c, "response.output_item.added", dto.ResponsesStreamResponse{Type: "response.output_item.added", OutputIndex: &index, ItemID: tool.ID, Item: &added}); err != nil {
		return err
	}
	if tool.Type == "custom_tool_call" {
		input := common.JsonRawMessageToString(tool.Input)
		if err := writeNativeResponsesSSE(c, "response.custom_tool_call_input.delta", dto.ResponsesStreamResponse{Type: "response.custom_tool_call_input.delta", OutputIndex: &index, ItemID: tool.ID, Delta: input}); err != nil {
			return err
		}
		if err := writeNativeResponsesSSE(c, "response.custom_tool_call_input.done", dto.ResponsesStreamResponse{Type: "response.custom_tool_call_input.done", OutputIndex: &index, ItemID: tool.ID}); err != nil {
			return err
		}
	} else {
		arguments := tool.ArgumentsString()
		if err := writeNativeResponsesSSE(c, "response.function_call_arguments.delta", dto.ResponsesStreamResponse{Type: "response.function_call_arguments.delta", OutputIndex: &index, ItemID: tool.ID, Delta: arguments}); err != nil {
			return err
		}
		if err := writeNativeResponsesSSE(c, "response.function_call_arguments.done", dto.ResponsesStreamResponse{Type: "response.function_call_arguments.done", OutputIndex: &index, ItemID: tool.ID}); err != nil {
			return err
		}
	}
	return writeNativeResponsesSSE(c, "response.output_item.done", dto.ResponsesStreamResponse{Type: "response.output_item.done", OutputIndex: &index, ItemID: tool.ID, Item: &tool})
}

func writeNativeResponsesSSE(c *gin.Context, eventType string, event dto.ResponsesStreamResponse) error {
	data, err := common.Marshal(event)
	if err != nil {
		return err
	}
	return helper.ResponseChunkData(c, event, string(data))
}

func isNativeResponsesTerminalEvent(eventType string) bool {
	switch eventType {
	case "response.output_text.done", "response.output_item.done", "response.completed", "response.incomplete", "response.failed", "error":
		return true
	default:
		return false
	}
}

func nativeResponsesUsage(response *dto.OpenAIResponsesResponse, info *relaycommon.RelayInfo) *dto.Usage {
	usage := &dto.Usage{}
	if response != nil && response.Usage != nil {
		copyNativeResponsesUsage(usage, response.Usage)
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 && info != nil {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func copyNativeResponsesUsage(dst, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	dst.PromptTokens = src.InputTokens
	dst.CompletionTokens = src.OutputTokens
	dst.TotalTokens = src.TotalTokens
	if src.InputTokensDetails != nil {
		dst.PromptTokensDetails = *src.InputTokensDetails
	}
}

func convertNativeResponsesOutput(outputs []dto.ResponsesOutput, toolMap map[string]nativeResponseTool) ([]dto.ResponsesOutput, bool) {
	converted := make([]dto.ResponsesOutput, 0, len(outputs))
	changed := false
	for _, output := range outputs {
		if output.Type != "message" {
			converted = append(converted, output)
			continue
		}
		message := output
		message.Content = make([]dto.ResponsesOutputContent, 0, len(output.Content))
		messageTools := make([]dto.ResponsesOutput, 0)
		messageChanged := false
		for _, content := range output.Content {
			if content.Type != "output_text" {
				message.Content = append(message.Content, content)
				continue
			}
			result := convertDSMLText(content.Text, toolMap)
			if len(result.Tools) == 0 {
				message.Content = append(message.Content, content)
				continue
			}
			messageChanged = true
			if result.Text != "" {
				content.Text = result.Text
				message.Content = append(message.Content, content)
			}
			messageTools = append(messageTools, result.Tools...)
		}
		if !messageChanged {
			converted = append(converted, output)
			continue
		}
		changed = true
		if len(message.Content) > 0 {
			converted = append(converted, message)
		}
		converted = append(converted, messageTools...)
	}
	return converted, changed
}

func stripDSMLToolsFromNativeOutput(outputs []dto.ResponsesOutput, toolMap map[string]nativeResponseTool) ([]dto.ResponsesOutput, bool) {
	originalCallIDs := make(map[string]bool)
	for _, output := range outputs {
		if output.CallId != "" {
			originalCallIDs[output.CallId] = true
		}
	}
	converted, changed := convertNativeResponsesOutput(outputs, toolMap)
	if !changed {
		return outputs, false
	}
	stripped := make([]dto.ResponsesOutput, 0, len(converted))
	for _, output := range converted {
		if (output.Type == "function_call" || output.Type == "custom_tool_call") && !originalCallIDs[output.CallId] {
			continue
		}
		stripped = append(stripped, output)
	}
	return stripped, true
}

func convertDSMLText(text string, toolMap map[string]nativeResponseTool) dsmlConversion {
	invocations, start, end, ok := parseDSMLInvocations(text)
	if !ok || len(invocations) == 0 {
		return dsmlConversion{Text: text}
	}
	tools := make([]dto.ResponsesOutput, 0, len(invocations))
	for _, invocation := range invocations {
		tool, exists := toolMap[invocation.Name]
		if !exists {
			return dsmlConversion{Text: text}
		}
		callID := "call_" + common.GetUUID()
		if tool.Type == "custom" {
			input, ok := invocationCustomInput(invocation.Parameters)
			if !ok {
				return dsmlConversion{Text: text}
			}
			tools = append(tools, dto.ResponsesOutput{Type: "custom_tool_call", ID: callID, Status: "completed", CallId: callID, Name: tool.Name, Input: rawString(input)})
			continue
		}
		arguments, err := common.Marshal(invocation.Parameters)
		if err != nil {
			return dsmlConversion{Text: text}
		}
		tools = append(tools, dto.ResponsesOutput{Type: "function_call", ID: callID, Status: "completed", CallId: callID, Name: tool.Name, Namespace: tool.Namespace, Arguments: rawString(string(arguments))})
	}
	return dsmlConversion{Text: strings.TrimSpace(text[:start] + text[end:]), Tools: tools}
}

func invocationCustomInput(parameters map[string]any) (string, bool) {
	if len(parameters) != 1 {
		return "", false
	}
	value, ok := parameters["input"]
	if !ok {
		return "", false
	}
	return common.Interface2String(value), true
}

func parseDSMLInvocations(text string) ([]dsmlInvocation, int, int, bool) {
	start, openEnd, closeStart, closeEnd, ok := findDSMLBlock(text, "tool_calls", 0)
	if !ok {
		start, openEnd, closeStart, closeEnd, ok = findDSMLBlock(text, "function_calls", 0)
	}
	if !ok {
		return nil, 0, 0, false
	}
	body := text[openEnd:closeStart]
	invocations := make([]dsmlInvocation, 0)
	position := 0
	for {
		invokeStart, invokeOpenEnd, invokeCloseStart, invokeCloseEnd, found := findDSMLBlock(body, "invoke", position)
		if !found {
			break
		}
		if strings.TrimSpace(body[position:invokeStart]) != "" {
			return nil, 0, 0, false
		}
		name := attributeValue(body[invokeStart:invokeOpenEnd], dsmlNameAttributePattern)
		if name == "" {
			return nil, 0, 0, false
		}
		parameters, ok := parseDSMLParameters(body[invokeOpenEnd:invokeCloseStart])
		if !ok {
			return nil, 0, 0, false
		}
		invocations = append(invocations, dsmlInvocation{Name: name, Parameters: parameters})
		position = invokeCloseEnd
	}
	if len(invocations) == 0 || strings.TrimSpace(body[position:]) != "" {
		return nil, 0, 0, false
	}
	return invocations, start, closeEnd, true
}

func parseDSMLParameters(body string) (map[string]any, bool) {
	parameters := make(map[string]any)
	position := 0
	for {
		remaining := body[position:]
		location := dsmlParameterOpenPattern.FindStringSubmatchIndex(remaining)
		if location == nil {
			break
		}
		openStart := position + location[0]
		openEnd := position + location[1]
		if strings.TrimSpace(body[position:openStart]) != "" {
			return nil, false
		}
		openTag := body[openStart:openEnd]
		name := attributeValue(openTag, dsmlNameAttributePattern)
		if name == "" {
			return nil, false
		}
		markerGroup := firstRegexpGroup(remaining, location, 1)
		marker := normalizeDSMLMarker(markerGroup)
		if marker == "" {
			return nil, false
		}
		closeStart, closeEnd, ok := findDSMLClose(body, "parameter", marker, openEnd)
		if !ok {
			return nil, false
		}
		value := body[openEnd:closeStart]
		stringValue := strings.EqualFold(attributeValue(openTag, dsmlStringAttributePattern), "true")
		if stringValue {
			parameters[name] = value
		} else {
			var parsed any
			if err := common.Unmarshal(common.StringToByteSlice(value), &parsed); err != nil {
				return nil, false
			}
			parameters[name] = parsed
		}
		position = closeEnd
	}
	if strings.TrimSpace(body[position:]) != "" {
		return nil, false
	}
	return parameters, true
}

func firstRegexpGroup(text string, indexes []int, group int) string {
	startIndex := group * 2
	if startIndex+1 >= len(indexes) || indexes[startIndex] < 0 || indexes[startIndex+1] < 0 {
		return ""
	}
	return text[indexes[startIndex]:indexes[startIndex+1]]
}

func findDSMLBlock(text, tag string, offset int) (int, int, int, int, bool) {
	for cursor := offset; cursor < len(text); {
		openStart := strings.Index(text[cursor:], "<")
		if openStart < 0 {
			return 0, 0, 0, 0, false
		}
		openStart += cursor
		openEndRelative := strings.Index(text[openStart:], ">")
		if openEndRelative < 0 {
			return 0, 0, 0, 0, false
		}
		openEnd := openStart + openEndRelative + 1
		marker, parsedTag, closing, ok := parseDSMLTag(text[openStart:openEnd])
		if ok && !closing && parsedTag == tag {
			closeStart, closeEnd, found := findDSMLClose(text, tag, marker, openEnd)
			return openStart, openEnd, closeStart, closeEnd, found
		}
		cursor = openEnd
	}
	return 0, 0, 0, 0, false
}

func findDSMLClose(text, tag, marker string, offset int) (int, int, bool) {
	for cursor := offset; cursor < len(text); {
		start := strings.Index(text[cursor:], "<")
		if start < 0 {
			return 0, 0, false
		}
		start += cursor
		endRelative := strings.Index(text[start:], ">")
		if endRelative < 0 {
			return 0, 0, false
		}
		end := start + endRelative + 1
		candidateMarker, candidateTag, closing, ok := parseDSMLTag(text[start:end])
		if ok && closing && candidateTag == tag && candidateMarker == marker {
			return start, end, true
		}
		cursor = end
	}
	return 0, 0, false
}

func parseDSMLTag(raw string) (string, string, bool, bool) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "<"), ">"))
	closing := strings.HasPrefix(inner, "/")
	if closing {
		inner = strings.TrimSpace(strings.TrimPrefix(inner, "/"))
	}
	fields := strings.Fields(inner)
	if len(fields) == 0 {
		return "", "", false, false
	}
	marker, tag, ok := splitDSMLMarkerTag(fields[0])
	if !ok {
		return "", "", false, false
	}
	return marker, tag, closing, true
}

func splitDSMLMarkerTag(token string) (string, string, bool) {
	token = normalizeDSMLPipes(token)
	index := strings.Index(strings.ToUpper(token), "DSML")
	if index < 0 {
		return "", "", false
	}
	marker := normalizeDSMLMarker(token[:index+len("DSML")])
	tag := strings.ToLower(strings.Trim(strings.TrimSpace(token[index+len("DSML"):]), "|"))
	if marker == "" || tag == "" {
		return "", "", false
	}
	return marker, tag, true
}

func normalizeDSMLMarker(marker string) string {
	marker = normalizeDSMLPipes(marker)
	index := strings.Index(strings.ToUpper(marker), "DSML")
	if index < 0 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(marker[:index+len("DSML")]))
}

func normalizeDSMLPipes(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '|', '｜', '│', '┃', '∣':
			return '|'
		default:
			return r
		}
	}, value)
}

func dsmlStartIndex(text string) int {
	upper := strings.ToUpper(text)
	for cursor := 0; cursor < len(text); {
		start := strings.Index(text[cursor:], "<")
		if start < 0 {
			return -1
		}
		start += cursor
		end := strings.Index(text[start:], ">")
		if end < 0 {
			if strings.Contains(upper[start:], "DSML") {
				return start
			}
			return -1
		}
		end += start + 1
		_, tag, closing, ok := parseDSMLTag(text[start:end])
		if ok && !closing && (tag == "tool_calls" || tag == "function_calls") {
			return start
		}
		cursor = end
	}
	return -1
}

func attributeValue(text string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) == 0 {
		return ""
	}
	for _, value := range match[1:] {
		if value != "" {
			return value
		}
	}
	return ""
}

func mergeNativeResponseTools(outputs []dto.ResponsesOutput, tools []dto.ResponsesOutput) []dto.ResponsesOutput {
	if len(tools) == 0 {
		return outputs
	}
	seen := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		if output.CallId != "" {
			seen[output.CallId] = true
		}
	}
	merged := append([]dto.ResponsesOutput(nil), outputs...)
	for _, tool := range tools {
		if tool.CallId == "" || seen[tool.CallId] {
			continue
		}
		seen[tool.CallId] = true
		merged = append(merged, tool)
	}
	return merged
}
