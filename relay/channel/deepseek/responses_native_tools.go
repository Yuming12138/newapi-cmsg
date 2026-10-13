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
	var appendTool func(map[string]any, string) error
	appendTool = func(tool map[string]any, namespace string) error {
		toolType := common.Interface2String(tool["type"])
		if toolType == "namespace" || toolType == "additional_tools" {
			if toolType == "namespace" {
				if name := strings.TrimSpace(common.Interface2String(tool["name"])); name != "" {
					namespace = name
				}
			}
			for _, child := range mapSlice(tool["tools"]) {
				if err := appendTool(child, namespace); err != nil {
					return err
				}
			}
			return nil
		}
		if toolType != "function" && toolType != "custom" {
			return nil
		}
		name := strings.TrimSpace(common.Interface2String(tool["name"]))
		if name == "" {
			return fmt.Errorf("DeepSeek Responses %s tool is missing a name", toolType)
		}
		name = namespacedToolName(namespace, name)
		if _, exists := seen[name]; exists {
			return nil
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
		return nil
	}
	for _, tool := range declared {
		if err := appendTool(tool, ""); err != nil {
			return request, err
		}
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

func nativeResponsesToolNameForUpstream(item map[string]any, toolMap map[string]nativeResponseTool) string {
	name := strings.TrimSpace(common.Interface2String(item["name"]))
	if namespace := strings.TrimSpace(common.Interface2String(item["namespace"])); namespace != "" {
		return namespacedToolName(namespace, name)
	}
	if tool, found := toolMap[name]; found {
		return namespacedToolName(tool.Namespace, tool.Name)
	}
	return name
}

func restoreNativeResponsesToolName(output *dto.ResponsesOutput, toolMap map[string]nativeResponseTool) bool {
	if output == nil || (output.Type != "function_call" && output.Type != "custom_tool_call") {
		return false
	}
	tool, found := toolMap[output.Name]
	if !found {
		return false
	}
	namespace := tool.Namespace
	if output.Type == "custom_tool_call" {
		// Codex custom calls identify the tool by name, without a namespace.
		namespace = ""
	}
	if output.Name == tool.Name && output.Namespace == namespace {
		return false
	}
	output.Name, output.Namespace = tool.Name, namespace
	return true
}
