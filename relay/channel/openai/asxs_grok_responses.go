package openai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const asxSGrokToolNamespaceMapKey = "asxs_grok_tool_namespace_map"

type asxsGrokToolRef struct {
	Namespace string
	Name      string
}

func isASXSGrokChannel(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	if info.ChannelId == 25 {
		return true
	}
	model := strings.ToLower(strings.TrimSpace(info.UpstreamModelName))
	return strings.HasPrefix(model, "grok-") && isASXSAPIBaseURL(info.ChannelBaseUrl)
}

func isASXSAPIBaseURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.asxs.top")
}

func namespacedToolName(namespace, name string) string {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "" || name == "" {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		return name
	}
	prefix := namespace
	if !strings.HasSuffix(prefix, "__") {
		prefix += "__"
	}
	if strings.HasPrefix(name, prefix) {
		return name
	}
	return prefix + name
}

func normalizeASXSGrokResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	if !isASXSGrokChannel(info) {
		if c != nil {
			c.Set(asxSGrokToolNamespaceMapKey, map[string]asxsGrokToolRef{})
		}
		return request, nil
	}

	tools, err := parseASXSGrokTools(request.Tools)
	if err != nil {
		return request, err
	}
	input, additionalTools, hasAdditionalTools, err := extractASXSGrokAdditionalTools(request.Input)
	if err != nil {
		return request, err
	}
	if hasAdditionalTools {
		request.Input = input
		tools = append(tools, additionalTools...)
	}

	flattened := make([]map[string]any, 0, len(tools))
	refs := make(map[string]asxsGrokToolRef)
	seenNames := make(map[string]struct{})
	for _, tool := range tools {
		if common.Interface2String(tool["type"]) != "namespace" {
			normalized, keep := normalizeASXSGrokTool(tool, "")
			if !keep {
				continue
			}
			if err := appendASXSGrokTool(&flattened, seenNames, normalized); err != nil {
				return request, err
			}
			continue
		}
		namespace := strings.TrimSpace(common.Interface2String(tool["name"]))
		children, ok := tool["tools"].([]any)
		if !ok || namespace == "" {
			return request, fmt.Errorf("invalid namespace tool %q", namespace)
		}
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if !ok {
				return request, fmt.Errorf("namespace %q contains a non-object tool", namespace)
			}
			normalized, keep := normalizeASXSGrokTool(child, namespace)
			if !keep {
				continue
			}
			childName := strings.TrimSpace(common.Interface2String(normalized["name"]))
			if childName == "" {
				continue
			}
			originalName := strings.TrimSpace(common.Interface2String(child["name"]))
			if err := appendASXSGrokTool(&flattened, seenNames, normalized); err != nil {
				return request, err
			}
			refs[childName] = asxsGrokToolRef{Namespace: namespace, Name: originalName}
		}
	}

	if len(flattened) == 0 {
		request.Tools = nil
		request.ToolChoice = nil
		request.ParallelToolCalls = nil
	} else {
		toolsJSON, err := common.Marshal(flattened)
		if err != nil {
			return request, err
		}
		request.Tools = toolsJSON
	}

	request.Input = normalizeASXSGrokResponsesInput(request.Input)
	request.ToolChoice = normalizeASXSGrokToolChoice(request.ToolChoice)
	if c != nil {
		c.Set(asxSGrokToolNamespaceMapKey, refs)
	}
	return request, nil
}

func parseASXSGrokTools(raw []byte) ([]map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var tools []map[string]any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	return tools, nil
}

func extractASXSGrokAdditionalTools(input []byte) ([]byte, []map[string]any, bool, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return input, nil, false, nil
	}

	var items []any
	if err := common.Unmarshal(input, &items); err != nil {
		return input, nil, false, fmt.Errorf("parse input items: %w", err)
	}
	kept := make([]any, 0, len(items))
	additional := make([]map[string]any, 0)
	found := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || common.Interface2String(item["type"]) != "additional_tools" {
			kept = append(kept, rawItem)
			continue
		}
		found = true
		rawTools, exists := item["tools"]
		if !exists {
			continue
		}
		toolItems, ok := rawTools.([]any)
		if !ok {
			return input, nil, false, fmt.Errorf("additional_tools.tools must be an array")
		}
		for _, rawTool := range toolItems {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				return input, nil, false, fmt.Errorf("additional_tools contains a non-object tool")
			}
			additional = append(additional, tool)
		}
	}
	if !found {
		return input, nil, false, nil
	}
	normalized, err := common.Marshal(kept)
	if err != nil {
		return input, nil, false, fmt.Errorf("marshal input items: %w", err)
	}
	return normalized, additional, true, nil
}

func normalizeASXSGrokTool(tool map[string]any, namespace string) (map[string]any, bool) {
	normalized := make(map[string]any, len(tool)+1)
	for key, value := range tool {
		normalized[key] = value
	}

	toolType := strings.TrimSpace(common.Interface2String(normalized["type"]))
	name := strings.TrimSpace(common.Interface2String(normalized["name"]))
	switch toolType {
	case "tool_search", "image_generation":
		return nil, false
	case "custom":
		if name == "apply_patch" {
			return nil, false
		}
		normalized["type"] = "function"
		delete(normalized, "format")
		if _, ok := normalized["parameters"]; !ok {
			normalized["parameters"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
	case "function":
		if _, ok := normalized["parameters"]; !ok {
			normalized["parameters"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
	}
	if namespace != "" && name != "" {
		normalized["name"] = namespacedToolName(namespace, name)
		delete(normalized, "namespace")
	}
	return normalized, true
}

func appendASXSGrokTool(tools *[]map[string]any, seen map[string]struct{}, tool map[string]any) error {
	name := strings.TrimSpace(common.Interface2String(tool["name"]))
	if name == "" {
		*tools = append(*tools, tool)
		return nil
	}
	if _, exists := seen[name]; exists {
		return fmt.Errorf("duplicate flattened tool name %q", name)
	}
	seen[name] = struct{}{}
	*tools = append(*tools, tool)
	return nil
}

func normalizeASXSGrokResponsesInput(input []byte) []byte {
	if len(input) == 0 {
		return input
	}
	var items []map[string]any
	if err := common.Unmarshal(input, &items); err != nil {
		return input
	}
	changed := false
	for _, item := range items {
		if common.Interface2String(item["type"]) != "function_call" {
			continue
		}
		namespace := strings.TrimSpace(common.Interface2String(item["namespace"]))
		name := strings.TrimSpace(common.Interface2String(item["name"]))
		if namespace == "" || name == "" {
			continue
		}
		item["name"] = namespacedToolName(namespace, name)
		delete(item, "namespace")
		changed = true
	}
	if !changed {
		return input
	}
	updated, err := common.Marshal(items)
	if err != nil {
		return input
	}
	return updated
}

func normalizeASXSGrokToolChoice(choice []byte) []byte {
	if len(choice) == 0 {
		return choice
	}
	var value map[string]any
	if err := common.Unmarshal(choice, &value); err != nil {
		return choice
	}
	if common.Interface2String(value["type"]) != "function" {
		return choice
	}
	namespace := strings.TrimSpace(common.Interface2String(value["namespace"]))
	name := strings.TrimSpace(common.Interface2String(value["name"]))
	if namespace == "" || name == "" {
		return choice
	}
	value["name"] = namespacedToolName(namespace, name)
	delete(value, "namespace")
	updated, err := common.Marshal(value)
	if err != nil {
		return choice
	}
	return updated
}

func asxSGrokToolNamespaceMap(c *gin.Context) map[string]asxsGrokToolRef {
	if c == nil {
		return nil
	}
	value, ok := c.Get(asxSGrokToolNamespaceMapKey)
	if !ok {
		return nil
	}
	refs, _ := value.(map[string]asxsGrokToolRef)
	return refs
}

func restoreASXSGrokResponsesBody(c *gin.Context, body []byte) []byte {
	refs := asxSGrokToolNamespaceMap(c)
	if len(refs) == 0 || len(body) == 0 {
		return body
	}
	var response map[string]any
	if err := common.Unmarshal(body, &response); err != nil {
		return body
	}
	if !restoreASXSGrokOutputItems(response["output"], refs) {
		return body
	}
	updated, err := common.Marshal(response)
	if err != nil {
		return body
	}
	return updated
}

func restoreASXSGrokStreamData(c *gin.Context, data string) string {
	refs := asxSGrokToolNamespaceMap(c)
	if len(refs) == 0 || strings.TrimSpace(data) == "" {
		return data
	}
	var event map[string]any
	if err := common.UnmarshalJsonStr(data, &event); err != nil {
		return data
	}
	changed := restoreASXSGrokOutputItems(event["item"], refs)
	if response, ok := event["response"].(map[string]any); ok {
		changed = restoreASXSGrokOutputItems(response["output"], refs) || changed
	}
	if !changed {
		return data
	}
	updated, err := common.Marshal(event)
	if err != nil {
		return data
	}
	return string(updated)
}

func restoreASXSGrokOutputItems(value any, refs map[string]asxsGrokToolRef) bool {
	changed := false
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if restoreASXSGrokOutputItems(item, refs) {
				changed = true
			}
		}
	case map[string]any:
		typ := common.Interface2String(items["type"])
		if typ == "function_call" {
			name := strings.TrimSpace(common.Interface2String(items["name"]))
			if ref, ok := refs[name]; ok {
				items["name"] = ref.Name
				items["namespace"] = ref.Namespace
				changed = true
			}
		}
	}
	return changed
}

func prepareASXSGrokResponseBody(resp *http.Response, c *gin.Context) {
	if resp == nil || resp.Body == nil {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(restoreASXSGrokResponsesBody(c, body)))
}
