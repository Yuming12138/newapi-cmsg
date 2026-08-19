package openai

import (
	"bytes"
	"io"
	"net/http"
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
	return info != nil && info.ChannelId == 25
}

func namespacedToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	if strings.HasSuffix(namespace, "_") || strings.HasPrefix(name, "_") {
		return namespace + name
	}
	return namespace + "_" + name
}

func normalizeASXSGrokResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (dto.OpenAIResponsesRequest, error) {
	if !isASXSGrokChannel(info) || len(request.Tools) == 0 {
		if c != nil {
			c.Set(asxSGrokToolNamespaceMapKey, map[string]asxsGrokToolRef{})
		}
		return request, nil
	}

	var tools []map[string]any
	if err := common.Unmarshal(request.Tools, &tools); err != nil {
		return request, err
	}

	flattened := make([]map[string]any, 0, len(tools))
	refs := make(map[string]asxsGrokToolRef)
	sawNamespace := false
	for _, tool := range tools {
		if common.Interface2String(tool["type"]) != "namespace" {
			flattened = append(flattened, tool)
			continue
		}
		sawNamespace = true

		namespace := strings.TrimSpace(common.Interface2String(tool["name"]))
		children, ok := tool["tools"].([]any)
		if !ok || namespace == "" {
			continue
		}
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if !ok || common.Interface2String(child["type"]) != "function" {
				continue
			}
			childName := strings.TrimSpace(common.Interface2String(child["name"]))
			if childName == "" {
				continue
			}
			flatName := namespacedToolName(namespace, childName)
			if _, exists := refs[flatName]; exists {
				continue
			}
			flatTool := make(map[string]any, len(child)+1)
			for key, value := range child {
				flatTool[key] = value
			}
			flatTool["type"] = "function"
			flatTool["name"] = flatName
			delete(flatTool, "namespace")
			flattened = append(flattened, flatTool)
			refs[flatName] = asxsGrokToolRef{Namespace: namespace, Name: childName}
		}
	}

	if !sawNamespace {
		if c != nil {
			c.Set(asxSGrokToolNamespaceMapKey, refs)
		}
		return request, nil
	}

	toolsJSON, err := common.Marshal(flattened)
	if err != nil {
		return request, err
	}
	request.Tools = toolsJSON
	request.Input = normalizeASXSGrokResponsesInput(request.Input, refs)
	request.ToolChoice = normalizeASXSGrokToolChoice(request.ToolChoice, refs)
	if c != nil {
		c.Set(asxSGrokToolNamespaceMapKey, refs)
	}
	return request, nil
}

func normalizeASXSGrokResponsesInput(input []byte, refs map[string]asxsGrokToolRef) []byte {
	if len(input) == 0 || len(refs) == 0 {
		return input
	}
	var items []map[string]any
	if err := common.Unmarshal(input, &items); err != nil {
		return input
	}
	changed := false
	for _, item := range items {
		namespace := strings.TrimSpace(common.Interface2String(item["namespace"]))
		name := strings.TrimSpace(common.Interface2String(item["name"]))
		if namespace == "" || name == "" {
			continue
		}
		flatName := namespacedToolName(namespace, name)
		if _, ok := refs[flatName]; !ok {
			continue
		}
		item["name"] = flatName
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

func normalizeASXSGrokToolChoice(choice []byte, refs map[string]asxsGrokToolRef) []byte {
	if len(choice) == 0 || len(refs) == 0 {
		return choice
	}
	var value map[string]any
	if err := common.Unmarshal(choice, &value); err != nil {
		return choice
	}
	namespace := strings.TrimSpace(common.Interface2String(value["namespace"]))
	name := strings.TrimSpace(common.Interface2String(value["name"]))
	if namespace == "" || name == "" {
		return choice
	}
	flatName := namespacedToolName(namespace, name)
	if _, ok := refs[flatName]; !ok {
		return choice
	}
	value["name"] = flatName
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
