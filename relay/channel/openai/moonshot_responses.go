package openai

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// Moonshot accepts custom apply_patch, but Codex also registers custom exec.
// Expose unsupported custom tools as functions upstream, then restore their
// custom-tool wire format on the way back to the client.
func isOfficialMoonshotURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.moonshot.cn")
}

func normalizeMoonshotResponsesRequest(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, map[string]struct{}, error) {
	customNames := make(map[string]struct{})
	if len(request.Tools) > 0 {
		var tools []map[string]any
		if err := common.Unmarshal(request.Tools, &tools); err != nil {
			return request, nil, fmt.Errorf("decode Moonshot tools: %w", err)
		}
		for _, tool := range tools {
			if err := normalizeMoonshotTool(tool, customNames); err != nil {
				return request, nil, err
			}
		}
		encoded, err := common.Marshal(tools)
		if err != nil {
			return request, nil, err
		}
		request.Tools = encoded
	}

	if input := bytes.TrimSpace(request.Input); len(input) > 0 && input[0] == '[' {
		var items []map[string]any
		if err := common.Unmarshal(request.Input, &items); err != nil {
			return request, nil, fmt.Errorf("decode Moonshot input: %w", err)
		}
		customCalls := make(map[string]struct{})
		filtered := make([]map[string]any, 0, len(items))
		for _, item := range items {
			switch item["type"] {
			case "reasoning":
				delete(item, "encrypted_content")
				summary, _ := item["summary"].([]any)
				content, _ := item["content"].([]any)
				if len(summary) == 0 && len(content) == 0 {
					continue
				}
			case "message":
				delete(item, "id")
			case "custom_tool_call":
				name, _ := item["name"].(string)
				if _, ok := customNames[name]; ok {
					rawInput, _ := item["input"].(string)
					arguments, err := common.Marshal(map[string]string{"input": rawInput})
					if err != nil {
						return request, nil, err
					}
					item["type"] = "function_call"
					item["arguments"] = string(arguments)
					delete(item, "input")
					delete(item, "id")
					if callID, _ := item["call_id"].(string); callID != "" {
						customCalls[callID] = struct{}{}
					}
				}
			}
			filtered = append(filtered, item)
		}
		for _, item := range filtered {
			if item["type"] != "custom_tool_call_output" {
				continue
			}
			callID, _ := item["call_id"].(string)
			if _, ok := customCalls[callID]; ok {
				item["type"] = "function_call_output"
				delete(item, "id")
			}
		}
		encoded, err := common.Marshal(filtered)
		if err != nil {
			return request, nil, err
		}
		request.Input = encoded
	}

	if request.Reasoning != nil {
		effort := strings.ToLower(strings.TrimSpace(request.Reasoning.Effort))
		switch effort {
		case "none", "minimal", "low":
			effort = "low"
		case "medium", "high":
			effort = "high"
		case "xhigh", "max", "ultra":
			effort = "max"
		case "":
			effort = "max"
		default:
			return request, nil, fmt.Errorf("unsupported Moonshot reasoning effort %q", effort)
		}
		request.Reasoning = &dto.Reasoning{Effort: effort}
	}
	if len(request.Include) > 0 {
		var includes []string
		if err := common.Unmarshal(request.Include, &includes); err != nil {
			return request, nil, fmt.Errorf("decode Moonshot include: %w", err)
		}
		filtered := make([]string, 0, len(includes))
		for _, include := range includes {
			if include == "web_search_call.results" || include == "web_search_call.action.sources" {
				filtered = append(filtered, include)
			}
		}
		request.Include = nil
		if len(filtered) > 0 {
			var err error
			request.Include, err = common.Marshal(filtered)
			if err != nil {
				return request, nil, err
			}
		}
	}
	if len(request.ToolChoice) > 0 {
		var choice string
		if err := common.Unmarshal(request.ToolChoice, &choice); err != nil || choice != "auto" {
			if choice == "none" {
				request.Tools = nil
				customNames = make(map[string]struct{})
			}
			request.ToolChoice = nil
		}
	}
	if len(request.Text) > 0 {
		var textConfig map[string]any
		if err := common.Unmarshal(request.Text, &textConfig); err != nil {
			return request, nil, fmt.Errorf("decode Moonshot text format: %w", err)
		}
		format, _ := textConfig["format"].(map[string]any)
		request.Text = nil
		if format["type"] == "json_schema" {
			var err error
			request.Text, err = common.Marshal(map[string]any{"format": format})
			if err != nil {
				return request, nil, err
			}
		}
	}
	// Keep only parameters described by Moonshot's Responses API.
	request.ClientMetadata = nil
	request.ContextManagement = nil
	request.Conversation = nil
	request.Metadata = nil
	request.ParallelToolCalls = nil
	request.PreviousResponseID = ""
	request.PromptCacheRetention = nil
	request.ServiceTier = nil
	request.Store = nil
	request.StreamOptions = nil
	request.Temperature = nil
	request.TopLogProbs = nil
	request.TopP = nil
	request.Truncation = nil
	request.User = nil
	request.MaxToolCalls = nil
	request.Prompt = nil
	request.EnableThinking = nil
	request.Preset = nil
	return request, customNames, nil
}

func normalizeMoonshotTool(tool map[string]any, customNames map[string]struct{}) error {
	toolType, _ := tool["type"].(string)
	if toolType == "namespace" {
		nested, _ := tool["tools"].([]any)
		for _, value := range nested {
			child, ok := value.(map[string]any)
			if ok {
				if err := normalizeMoonshotTool(child, customNames); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if toolType != "custom" {
		return nil
	}
	name, _ := tool["name"].(string)
	if name == "apply_patch" {
		return nil
	}
	if name == "" {
		return fmt.Errorf("Moonshot custom tool is missing a name")
	}
	customNames[name] = struct{}{}
	description, _ := tool["description"].(string)
	tool["type"] = "function"
	tool["description"] = description + "\nPass the original free-form tool input as the input string."
	tool["parameters"] = map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"input": map[string]any{"type": "string"}},
		"required":             []string{"input"},
		"additionalProperties": false,
	}
	delete(tool, "format")
	return nil
}

type moonshotCallState struct {
	id        string
	arguments strings.Builder
	emitted   bool
}

type moonshotResponsesBody struct {
	source         io.ReadCloser
	reader         *bufio.Reader
	pending        []byte
	calls          map[int]*moonshotCallState
	names          map[string]struct{}
	sequenceOffset int
}

func rewriteMoonshotResponses(resp *http.Response, streamed bool, names map[string]struct{}) error {
	if resp == nil || resp.Body == nil || len(names) == 0 {
		return nil
	}
	if streamed {
		resp.Body = &moonshotResponsesBody{
			source: resp.Body, reader: bufio.NewReader(resp.Body),
			calls: make(map[int]*moonshotCallState), names: names,
		}
		return nil
	}
	original, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	updated, err := rewriteMoonshotResponseJSON(original, names)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(updated))
	resp.ContentLength = int64(len(updated))
	resp.Header.Set("Content-Length", fmt.Sprint(len(updated)))
	return nil
}

func rewriteMoonshotResponseJSON(body []byte, names map[string]struct{}) ([]byte, error) {
	var response map[string]any
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if !rewriteMoonshotOutput(response["output"], names) {
		return body, nil
	}
	return common.Marshal(response)
}

func rewriteMoonshotOutput(value any, names map[string]struct{}) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	changed := false
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if rewriteMoonshotFunctionItem(item, names) {
			changed = true
		}
	}
	return changed
}

func rewriteMoonshotFunctionItem(item map[string]any, names map[string]struct{}) bool {
	if item["type"] != "function_call" {
		return false
	}
	name, _ := item["name"].(string)
	if _, ok := names[name]; !ok {
		return false
	}
	arguments, _ := item["arguments"].(string)
	item["type"] = "custom_tool_call"
	item["input"] = moonshotFunctionInput(arguments)
	delete(item, "arguments")
	if id, _ := item["id"].(string); strings.HasPrefix(id, "fc_") {
		item["id"] = "ctc_" + strings.TrimPrefix(id, "fc_")
	}
	return true
}

func moonshotFunctionInput(arguments string) string {
	var parsed map[string]any
	if err := common.Unmarshal([]byte(arguments), &parsed); err == nil {
		if input, ok := parsed["input"].(string); ok {
			return input
		}
	}
	return arguments
}

func (body *moonshotResponsesBody) Close() error { return body.source.Close() }

func (body *moonshotResponsesBody) Read(p []byte) (int, error) {
	for len(body.pending) == 0 {
		frame, err := body.readFrame()
		if err != nil && len(frame) == 0 {
			return 0, err
		}
		updated, rewriteErr := body.rewriteFrame(frame)
		if rewriteErr != nil {
			return 0, rewriteErr
		}
		body.pending = updated
		if err == io.EOF && len(body.pending) == 0 {
			return 0, io.EOF
		}
	}
	count := copy(p, body.pending)
	body.pending = body.pending[count:]
	return count, nil
}

func (body *moonshotResponsesBody) readFrame() ([]byte, error) {
	var frame bytes.Buffer
	for {
		line, err := body.reader.ReadBytes('\n')
		frame.Write(line)
		if len(bytes.TrimSpace(line)) == 0 || err != nil {
			return frame.Bytes(), err
		}
	}
}

func (body *moonshotResponsesBody) rewriteFrame(frame []byte) ([]byte, error) {
	var payload []byte
	for _, line := range bytes.Split(frame, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("data:")) {
			payload = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			break
		}
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return frame, nil
	}
	var event map[string]any
	if err := common.Unmarshal(payload, &event); err != nil {
		return frame, nil
	}
	eventType, _ := event["type"].(string)
	sequence, hasSequence := event["sequence_number"].(float64)
	index := moonshotOutputIndex(event["output_index"])
	switch eventType {
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if item != nil && rewriteMoonshotFunctionItem(item, body.names) {
			id, _ := item["id"].(string)
			body.calls[index] = &moonshotCallState{id: id}
			if _, ok := event["item_id"]; ok {
				event["item_id"] = id
			}
			return body.encodeEvents(sequence, hasSequence, event)
		}
	case "response.function_call_arguments.delta":
		if call := body.calls[index]; call != nil {
			delta, _ := event["delta"].(string)
			call.arguments.WriteString(delta)
			body.sequenceOffset--
			return nil, nil
		}
	case "response.function_call_arguments.done":
		if call := body.calls[index]; call != nil {
			arguments, _ := event["arguments"].(string)
			if arguments == "" {
				arguments = call.arguments.String()
			}
			return body.encodeEvents(sequence, hasSequence, body.customInputEvents(index, call, moonshotFunctionInput(arguments))...)
		}
	case "response.output_item.done":
		item, _ := event["item"].(map[string]any)
		if item != nil && rewriteMoonshotFunctionItem(item, body.names) {
			if _, ok := event["item_id"]; ok {
				event["item_id"] = item["id"]
			}
			if call := body.calls[index]; call != nil && !call.emitted {
				input, _ := item["input"].(string)
				return body.encodeEvents(sequence, hasSequence, append(body.customInputEvents(index, call, input), event)...)
			}
			return body.encodeEvents(sequence, hasSequence, event)
		}
	case "response.completed":
		if response, ok := event["response"].(map[string]any); ok && rewriteMoonshotOutput(response["output"], body.names) {
			return body.encodeEvents(sequence, hasSequence, event)
		}
	}
	if body.sequenceOffset != 0 {
		return body.encodeEvents(sequence, hasSequence, event)
	}
	return frame, nil
}

func (body *moonshotResponsesBody) customInputEvents(index int, call *moonshotCallState, input string) []map[string]any {
	call.emitted = true
	return []map[string]any{
		{
			"type": "response.custom_tool_call_input.delta", "output_index": index,
			"item_id": call.id, "delta": input,
		},
		{
			"type": "response.custom_tool_call_input.done", "output_index": index,
			"item_id": call.id, "input": input,
		},
	}
}

// A single upstream function arguments event can become two custom input events.
// Renumber emitted events so sequence_number stays present and strictly ordered.
func (body *moonshotResponsesBody) encodeEvents(sequence float64, hasSequence bool, events ...map[string]any) ([]byte, error) {
	var encoded []byte
	for i, event := range events {
		if hasSequence {
			event["sequence_number"] = sequence + float64(body.sequenceOffset+i)
		}
		frame, err := encodeMoonshotEvent(event)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, frame...)
	}
	body.sequenceOffset += len(events) - 1
	return encoded, nil
}

func moonshotOutputIndex(value any) int {
	if number, ok := value.(float64); ok {
		return int(number)
	}
	return 0
}

func encodeMoonshotEvent(event map[string]any) ([]byte, error) {
	payload, err := common.Marshal(event)
	if err != nil {
		return nil, err
	}
	eventType, _ := event["type"].(string)
	return []byte("event: " + eventType + "\ndata: " + string(payload) + "\n\n"), nil
}
