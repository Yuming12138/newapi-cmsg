package deepseek

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// DeepSeek ignores input.additional_tools and only advertises function tools.
// Promote Codex's dynamically registered tools before sending the request.
func normalizeNativeResponsesToolsForUpstream(request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	var declared []map[string]any
	if len(request.Tools) > 0 && common.GetJsonType(request.Tools) != "null" {
		if err := common.Unmarshal(request.Tools, &declared); err != nil {
			return request, fmt.Errorf("decode DeepSeek Responses tools: %w", err)
		}
	}
	items, err := normalizeResponsesInput(request.Input)
	if err != nil {
		return request, err
	}
	for _, item := range items {
		if common.Interface2String(item["type"]) == "additional_tools" {
			declared = append(declared, mapSlice(item["tools"])...)
		}
	}

	tools := make([]map[string]any, 0, len(declared))
	seen := make(map[string]struct{})
	for _, tool := range declared {
		toolType := common.Interface2String(tool["type"])
		if toolType != "function" && toolType != "custom" {
			continue
		}
		name := strings.TrimSpace(common.Interface2String(tool["name"]))
		if name == "" {
			return request, fmt.Errorf("DeepSeek Responses %s tool is missing a name", toolType)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		description := common.Interface2String(tool["description"])
		parameters := tool["parameters"]
		if toolType == "custom" {
			description += "\nPass the original free-form tool input as the input string."
			parameters = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"input": map[string]any{"type": "string"}},
				"required":             []string{"input"},
				"additionalProperties": false,
			}
		}
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, map[string]any{
			"type": "function", "name": name, "description": description, "parameters": parameters,
		})
	}
	request.Tools = nil
	if len(tools) > 0 {
		request.Tools, err = common.Marshal(tools)
		if err != nil {
			return request, err
		}
	}
	return request, nil
}
