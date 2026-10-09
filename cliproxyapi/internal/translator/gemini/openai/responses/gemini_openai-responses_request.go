package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const geminiResponsesThoughtSignature = "skip_thought_signature_validator"

func ConvertOpenAIResponsesRequestToGemini(modelName string, inputRawJSON []byte, stream bool) []byte {
	rawJSON := inputRawJSON

	// Note: stream parameter is part of the fixed method signature
	useGeminiNativeReasoningLayout := sigcompat.SignatureProviderFromModelName(modelName) == sigcompat.SignatureProviderGemini
	_ = stream // Unused but required by interface

	// Base Gemini API template (do not include thinkingConfig by default)
	out := []byte(`{"contents":[]}`)

	root := gjson.ParseBytes(rawJSON)

	// Extract system instruction from OpenAI "instructions" field
	if instructions := root.Get("instructions"); instructions.Exists() {
		systemInstr := []byte(`{"parts":[{"text":""}]}`)
		systemInstr, _ = sjson.SetBytes(systemInstr, "parts.0.text", instructions.String())
		out, _ = sjson.SetRawBytes(out, "systemInstruction", systemInstr)
	}

	// Convert input messages to Gemini contents format
	if input := root.Get("input"); input.Exists() && input.IsArray() {
		items := input.Array()

		// Normalize consecutive function calls and outputs so each call is immediately followed by its response
		normalized := make([]gjson.Result, 0, len(items))
		for i := 0; i < len(items); {
			item := items[i]
			itemType := item.Get("type").String()
			itemRole := item.Get("role").String()
			if itemType == "" && itemRole != "" {
				itemType = "message"
			}

			if itemType == "additional_tools" {
				i++
				continue
			}

			if itemType == "function_call" || itemType == "custom_tool_call" {
				var calls []gjson.Result
				var outputs []gjson.Result

				for i < len(items) {
					next := items[i]
					nextType := next.Get("type").String()
					nextRole := next.Get("role").String()
					if nextType == "" && nextRole != "" {
						nextType = "message"
					}
					if nextType != "function_call" && nextType != "custom_tool_call" {
						break
					}
					calls = append(calls, next)
					i++
				}

				for i < len(items) {
					next := items[i]
					nextType := next.Get("type").String()
					nextRole := next.Get("role").String()
					if nextType == "" && nextRole != "" {
						nextType = "message"
					}
					if nextType != "function_call_output" && nextType != "custom_tool_call_output" {
						break
					}
					outputs = append(outputs, next)
					i++
				}

				if len(calls) > 0 {
					outputMap := make(map[string]gjson.Result, len(outputs))
					for _, outItem := range outputs {
						outputMap[outItem.Get("call_id").String()] = outItem
					}
					for _, call := range calls {
						normalized = append(normalized, call)
						callID := call.Get("call_id").String()
						if resp, ok := outputMap[callID]; ok {
							normalized = append(normalized, resp)
							delete(outputMap, callID)
						}
					}
					for _, outItem := range outputs {
						if _, ok := outputMap[outItem.Get("call_id").String()]; ok {
							normalized = append(normalized, outItem)
						}
					}
					continue
				}
			}

			if itemType == "function_call_output" || itemType == "custom_tool_call_output" {
				normalized = append(normalized, item)
				i++
				continue
			}

			normalized = append(normalized, item)
			i++
		}

		for i := 0; i < len(normalized); i++ {
			item := normalized[i]
			itemType := item.Get("type").String()
			itemRole := item.Get("role").String()
			if itemType == "" && itemRole != "" {
				itemType = "message"
			}

			switch itemType {
			case "message":
				if strings.EqualFold(itemRole, "system") || strings.EqualFold(itemRole, "developer") {
					if contentArray := item.Get("content"); contentArray.Exists() {
						systemInstr := []byte(`{"parts":[]}`)
						if systemInstructionResult := gjson.GetBytes(out, "systemInstruction"); systemInstructionResult.Exists() {
							systemInstr = []byte(systemInstructionResult.Raw)
						}

						if contentArray.IsArray() {
							contentArray.ForEach(func(_, contentItem gjson.Result) bool {
								part := []byte(`{"text":""}`)
								text := contentItem.Get("text").String()
								part, _ = sjson.SetBytes(part, "text", text)
								systemInstr, _ = sjson.SetRawBytes(systemInstr, "parts.-1", part)
								return true
							})
						} else if contentArray.Type == gjson.String {
							part := []byte(`{"text":""}`)
							part, _ = sjson.SetBytes(part, "text", contentArray.String())
							systemInstr, _ = sjson.SetRawBytes(systemInstr, "parts.-1", part)
						}

						if gjson.GetBytes(systemInstr, "parts.#").Int() > 0 {
							out, _ = sjson.SetRawBytes(out, "systemInstruction", systemInstr)
						}
					}
					continue
				}

				// Handle regular messages
				// Note: In Responses format, model outputs may appear as content items with type "output_text"
				// even when the message.role is "user". We split such items into distinct Gemini messages
				// with roles derived from the content type to match docs/convert-2.md.
				if contentArray := item.Get("content"); contentArray.Exists() && contentArray.IsArray() {
					currentRole := ""
					currentParts := make([][]byte, 0)

					flush := func() {
						if currentRole == "" || len(currentParts) == 0 {
							currentParts = currentParts[:0]
							return
						}
						one := []byte(`{"role":"","parts":[]}`)
						one, _ = sjson.SetBytes(one, "role", currentRole)
						for _, part := range currentParts {
							one, _ = sjson.SetRawBytes(one, "parts.-1", part)
						}
						out, _ = sjson.SetRawBytes(out, "contents.-1", one)
						currentParts = currentParts[:0]
					}

					contentArray.ForEach(func(_, contentItem gjson.Result) bool {
						contentType := contentItem.Get("type").String()
						if contentType == "" {
							contentType = "input_text"
						}

						effRole := "user"
						if itemRole != "" {
							switch strings.ToLower(itemRole) {
							case "assistant", "model":
								effRole = "model"
							default:
								effRole = strings.ToLower(itemRole)
							}
						}
						if contentType == "output_text" {
							effRole = "model"
						}
						if effRole == "assistant" {
							effRole = "model"
						}

						if currentRole != "" && effRole != currentRole {
							flush()
							currentRole = ""
						}
						if currentRole == "" {
							currentRole = effRole
						}

						var partJSON []byte
						switch contentType {
						case "input_text", "output_text":
							if text := contentItem.Get("text"); text.Exists() {
								partJSON = []byte(`{"text":""}`)
								partJSON, _ = sjson.SetBytes(partJSON, "text", text.String())
							}
						case "input_image":
							imageURL := contentItem.Get("image_url").String()
							if imageURL == "" {
								imageURL = contentItem.Get("url").String()
							}
							if imageURL != "" {
								mimeType := "application/octet-stream"
								data := ""
								if strings.HasPrefix(imageURL, "data:") {
									trimmed := strings.TrimPrefix(imageURL, "data:")
									mediaAndData := strings.SplitN(trimmed, ";base64,", 2)
									if len(mediaAndData) == 2 {
										if mediaAndData[0] != "" {
											mimeType = mediaAndData[0]
										}
										data = mediaAndData[1]
									} else {
										mediaAndData = strings.SplitN(trimmed, ",", 2)
										if len(mediaAndData) == 2 {
											if mediaAndData[0] != "" {
												mimeType = mediaAndData[0]
											}
											data = mediaAndData[1]
										}
									}
								}
								if data != "" {
									partJSON = []byte(`{"inline_data":{"mime_type":"","data":""}}`)
									partJSON, _ = sjson.SetBytes(partJSON, "inline_data.mime_type", mimeType)
									partJSON, _ = sjson.SetBytes(partJSON, "inline_data.data", data)
								}
							}
						case "input_audio":
							audioData := contentItem.Get("data").String()
							audioFormat := contentItem.Get("format").String()
							if audioData != "" {
								audioMimeMap := map[string]string{
									"mp3":       "audio/mpeg",
									"wav":       "audio/wav",
									"ogg":       "audio/ogg",
									"flac":      "audio/flac",
									"aac":       "audio/aac",
									"webm":      "audio/webm",
									"pcm16":     "audio/pcm",
									"g711_ulaw": "audio/basic",
									"g711_alaw": "audio/basic",
								}
								mimeType := "audio/wav"
								if audioFormat != "" {
									if mapped, ok := audioMimeMap[audioFormat]; ok {
										mimeType = mapped
									} else {
										mimeType = "audio/" + audioFormat
									}
								}
								partJSON = []byte(`{"inline_data":{"mime_type":"","data":""}}`)
								partJSON, _ = sjson.SetBytes(partJSON, "inline_data.mime_type", mimeType)
								partJSON, _ = sjson.SetBytes(partJSON, "inline_data.data", audioData)
							}
						}

						if len(partJSON) > 0 {
							currentParts = append(currentParts, partJSON)
						}
						return true
					})

					flush()
				} else if contentArray.Type == gjson.String {
					effRole := "user"
					if itemRole != "" {
						switch strings.ToLower(itemRole) {
						case "assistant", "model":
							effRole = "model"
						default:
							effRole = strings.ToLower(itemRole)
						}
					}

					one := []byte(`{"role":"","parts":[{"text":""}]}`)
					one, _ = sjson.SetBytes(one, "role", effRole)
					one, _ = sjson.SetBytes(one, "parts.0.text", contentArray.String())
					out, _ = sjson.SetRawBytes(out, "contents.-1", one)
				}

			case "function_call", "custom_tool_call":
				// Handle function / custom tool calls - convert to model message with functionCall
				rawName := item.Get("name").String()
				ns := item.Get("namespace").String()
				name := util.SanitizeFunctionName(namespacedToolName(ns, rawName))

				modelContent := []byte(`{"role":"model","parts":[]}`)
				functionCall := []byte(`{"functionCall":{"name":"","args":{}}}`)
				functionCall, _ = sjson.SetBytes(functionCall, "functionCall.name", name)
				functionCall, _ = sjson.SetBytes(functionCall, "thoughtSignature", geminiResponsesThoughtSignature)
				functionCall, _ = sjson.SetBytes(functionCall, "functionCall.id", item.Get("call_id").String())

				if itemType == "function_call" {
					arguments := item.Get("arguments").String()
					if arguments != "" {
						argsResult := gjson.Parse(arguments)
						if argsResult.Exists() && argsResult.Type == gjson.JSON {
							functionCall, _ = sjson.SetRawBytes(functionCall, "functionCall.args", []byte(argsResult.Raw))
						}
					}
				} else {
					inputStr := item.Get("input").String()
					if inputStr != "" {
						argsResult := gjson.Parse(inputStr)
						if argsResult.Exists() && argsResult.Type == gjson.JSON && strings.HasPrefix(strings.TrimSpace(inputStr), "{") {
							functionCall, _ = sjson.SetRawBytes(functionCall, "functionCall.args", []byte(argsResult.Raw))
						} else {
							functionCall, _ = sjson.SetBytes(functionCall, "functionCall.args.input", inputStr)
						}
					}
				}

				modelContent, _ = sjson.SetRawBytes(modelContent, "parts.-1", functionCall)
				out, _ = sjson.SetRawBytes(out, "contents.-1", modelContent)

			case "function_call_output", "custom_tool_call_output":
				// Handle function / custom call outputs - convert to function message with functionResponse
				callID := item.Get("call_id").String()
				outputResult := item.Get("output")

				functionContent := []byte(`{"role":"function","parts":[]}`)
				functionResponse := []byte(`{"functionResponse":{"name":"","response":{}}}`)

				functionName := "unknown"
				// Find the corresponding function call name by matching call_id
				if inputArray := root.Get("input"); inputArray.Exists() && inputArray.IsArray() {
					inputArray.ForEach(func(_, prevItem gjson.Result) bool {
						prevType := prevItem.Get("type").String()
						if (prevType == "function_call" || prevType == "custom_tool_call") && prevItem.Get("call_id").String() == callID {
							prevName := prevItem.Get("name").String()
							prevNs := prevItem.Get("namespace").String()
							functionName = namespacedToolName(prevNs, prevName)
							return false
						}
						return true
					})
				}
				functionName = util.SanitizeFunctionName(functionName)

				functionResponse, _ = sjson.SetBytes(functionResponse, "functionResponse.name", functionName)
				functionResponse, _ = sjson.SetBytes(functionResponse, "functionResponse.id", callID)

				if outputResult.Exists() {
					if (outputResult.IsArray() || outputResult.IsObject()) && json.Valid([]byte(outputResult.Raw)) {
						functionResponse, _ = sjson.SetRawBytes(functionResponse, "functionResponse.response.result", []byte(outputResult.Raw))
					} else {
						functionResponse, _ = sjson.SetBytes(functionResponse, "functionResponse.response.result", outputResult.String())
					}
				}
				functionContent, _ = sjson.SetRawBytes(functionContent, "parts.-1", functionResponse)
				out, _ = sjson.SetRawBytes(out, "contents.-1", functionContent)

			case "reasoning":
				thoughtText := item.Get("summary.0.text").String()
				signature := openAIResponsesGeminiThoughtSignature(item.Get("encrypted_content").String())

				visibleText := ""
				if useGeminiNativeReasoningLayout && i+1 < len(normalized) {
					next := normalized[i+1]
					if visible, ok := openAIResponsesAssistantVisibleText(next); ok {
						visibleText = visible
						i++
					}
				}

				modelContent := buildOpenAIResponsesReasoningModelContent(thoughtText, visibleText, signature, useGeminiNativeReasoningLayout)
				out, _ = sjson.SetRawBytes(out, "contents.-1", modelContent)
			}
		}
	} else if input.Exists() && input.Type == gjson.String {
		// Simple string input conversion to user message
		userContent := []byte(`{"role":"user","parts":[{"text":""}]}`)
		userContent, _ = sjson.SetBytes(userContent, "parts.0.text", input.String())
		out, _ = sjson.SetRawBytes(out, "contents.-1", userContent)
	}

	// Gemini/Vertex accepts assistant/model turns in history, but some model
	// surfaces reject requests whose final turn is model-authored prefill.
	// Preserve reasoning history (thought parts); only strip trailing plain model text.
	contents := gjson.GetBytes(out, "contents")
	if contents.Exists() && contents.IsArray() {
		arr := contents.Array()
		if len(arr) > 0 && shouldStripTrailingOpenAIResponsesModelPrefill(arr[len(arr)-1]) {
			out, _ = sjson.DeleteBytes(out, fmt.Sprintf("contents.%d", len(arr)-1))
		}
	}

	// Convert tools to Gemini functionDeclarations format (from tools and input.additional_tools)
	allTools := extractAllOpenAIResponsesTools(root)
	if len(allTools) > 0 {
		geminiTools := []byte(`[{"functionDeclarations":[]}]`)
		seenDecls := make(map[string]int)

		for _, tool := range allTools {
			fullName := namespacedToolName(tool.Namespace, tool.Name)
			sanitizedName := util.SanitizeFunctionName(fullName)

			funcDecl := []byte(`{"name":"","description":"","parametersJsonSchema":{}}`)
			funcDecl, _ = sjson.SetBytes(funcDecl, "name", sanitizedName)

			if tool.Type == "custom" {
				desc := tool.Description
				if desc == "" {
					desc = "Execute custom command or JavaScript code."
				}
				if tool.Name == "apply_patch" {
					desc = "Apply a patch to create, modify, or delete files. Pass the patch content in unified diff or patch format via 'input' or 'patch'."
					funcDecl, _ = sjson.SetBytes(funcDecl, "description", desc)
					patchSchema := `{"type":"object","properties":{"input":{"type":"string","description":"Patch content in unified diff format (e.g. *** Begin Patch ...)"},"patch":{"type":"string","description":"Patch content in unified diff format"}},"required":["input"]}`
					funcDecl, _ = sjson.SetRawBytes(funcDecl, "parametersJsonSchema", []byte(patchSchema))
				} else if tool.Name == "exec" {
					desc += "\nYou can pass 'cmd' with a shell command to execute directly (e.g. bash or powershell script to create/edit files or run commands), or 'code' / 'input' with JavaScript source code."
					funcDecl, _ = sjson.SetBytes(funcDecl, "description", desc)
					customSchema := `{"type":"object","properties":{"cmd":{"type":"string","description":"Shell command to run directly (e.g. creating/modifying files or running scripts)"},"code":{"type":"string","description":"JavaScript code to execute"},"input":{"type":"string","description":"Tool input string"}}}`
					funcDecl, _ = sjson.SetRawBytes(funcDecl, "parametersJsonSchema", []byte(customSchema))
				} else {
					desc += "\nPass tool parameters via 'input'."
					funcDecl, _ = sjson.SetBytes(funcDecl, "description", desc)
					genericSchema := `{"type":"object","properties":{"input":{"type":"string","description":"Tool input string"}}}`
					funcDecl, _ = sjson.SetRawBytes(funcDecl, "parametersJsonSchema", []byte(genericSchema))
				}
			} else {
				if tool.Description != "" {
					funcDecl, _ = sjson.SetBytes(funcDecl, "description", tool.Description)
				}
				if tool.Parameters != "" {
					funcDecl, _ = sjson.SetRawBytes(funcDecl, "parametersJsonSchema", []byte(util.CleanJSONSchemaForGemini(tool.Parameters)))
				}
			}

			if idx, exists := seenDecls[sanitizedName]; exists {
				geminiTools, _ = sjson.SetRawBytes(geminiTools, fmt.Sprintf("0.functionDeclarations.%d", idx), funcDecl)
			} else {
				seenDecls[sanitizedName] = len(seenDecls)
				geminiTools, _ = sjson.SetRawBytes(geminiTools, "0.functionDeclarations.-1", funcDecl)
			}
		}

		if len(seenDecls) > 0 {
			out, _ = sjson.SetRawBytes(out, "tools", geminiTools)
		}
	}

	// Handle generation config from OpenAI format
	if maxOutputTokens := root.Get("max_output_tokens"); maxOutputTokens.Exists() {
		genConfig := []byte(`{"maxOutputTokens":0}`)
		genConfig, _ = sjson.SetBytes(genConfig, "maxOutputTokens", maxOutputTokens.Int())
		out, _ = sjson.SetRawBytes(out, "generationConfig", genConfig)
	}

	// Handle temperature if present
	if temperature := root.Get("temperature"); temperature.Exists() {
		if !gjson.GetBytes(out, "generationConfig").Exists() {
			out, _ = sjson.SetRawBytes(out, "generationConfig", []byte(`{}`))
		}
		out, _ = sjson.SetBytes(out, "generationConfig.temperature", temperature.Float())
	}

	// Handle top_p if present
	if topP := root.Get("top_p"); topP.Exists() {
		if !gjson.GetBytes(out, "generationConfig").Exists() {
			out, _ = sjson.SetRawBytes(out, "generationConfig", []byte(`{}`))
		}
		out, _ = sjson.SetBytes(out, "generationConfig.topP", topP.Float())
	}

	// Handle stop sequences
	if stopSequences := root.Get("stop_sequences"); stopSequences.Exists() && stopSequences.IsArray() {
		if !gjson.GetBytes(out, "generationConfig").Exists() {
			out, _ = sjson.SetRawBytes(out, "generationConfig", []byte(`{}`))
		}
		var sequences []string
		stopSequences.ForEach(func(_, seq gjson.Result) bool {
			sequences = append(sequences, seq.String())
			return true
		})
		out, _ = sjson.SetBytes(out, "generationConfig.stopSequences", sequences)
	}

	out = applyOpenAIResponsesTextFormatToGemini(out, root)

	// Apply thinking configuration: convert OpenAI Responses API reasoning.effort to Gemini thinkingConfig.
	// Inline translation-only mapping; capability checks happen later in ApplyThinking.
	re := root.Get("reasoning.effort")
	if re.Exists() {
		effort := strings.ToLower(strings.TrimSpace(re.String()))
		if effort != "" {
			thinkingPath := "generationConfig.thinkingConfig"
			if effort == "auto" {
				out, _ = sjson.SetBytes(out, thinkingPath+".thinkingBudget", -1)
				out, _ = sjson.SetBytes(out, thinkingPath+".includeThoughts", true)
			} else {
				out, _ = sjson.SetBytes(out, thinkingPath+".thinkingLevel", effort)
				out, _ = sjson.SetBytes(out, thinkingPath+".includeThoughts", effort != "none")
			}
		}
	}

	result := out
	result = common.AttachDefaultSafetySettings(result, "safetySettings")
	result = common.EnsureAntigravityBudget(result, "contents", modelName)
	return result
}

func shouldStripTrailingOpenAIResponsesModelPrefill(lastContent gjson.Result) bool {
	if lastContent.Get("role").String() != "model" {
		return false
	}
	parts := lastContent.Get("parts")
	if !parts.IsArray() {
		return false
	}
	for _, part := range parts.Array() {
		if part.Get("thought").Bool() {
			return false
		}
	}
	return true
}

func isTrailingOpenAIResponsesAssistantPrefill(items []gjson.Result, assistantIndex int) bool {
	if assistantIndex < 0 || assistantIndex >= len(items) {
		return false
	}
	for j := assistantIndex + 1; j < len(items); j++ {
		itemType := items[j].Get("type").String()
		itemRole := items[j].Get("role").String()
		if itemType == "" && itemRole != "" {
			itemType = "message"
		}
		switch itemType {
		case "reasoning", "function_call", "function_call_output":
			return false
		case "message":
			if strings.EqualFold(itemRole, "system") || strings.EqualFold(itemRole, "developer") {
				continue
			}
			return false
		}
	}
	_, ok := openAIResponsesAssistantVisibleText(items[assistantIndex])
	return ok
}

func openAIResponsesAssistantVisibleText(item gjson.Result) (string, bool) {
	itemType := item.Get("type").String()
	itemRole := item.Get("role").String()
	if itemType == "" && itemRole != "" {
		itemType = "message"
	}
	if itemType != "message" {
		return "", false
	}

	content := item.Get("content")
	if !content.Exists() {
		return "", false
	}
	if content.Type == gjson.String {
		switch strings.ToLower(strings.TrimSpace(itemRole)) {
		case "assistant", "model":
			return content.String(), true
		default:
			return "", false
		}
	}
	if !content.IsArray() {
		return "", false
	}

	var textParts []string
	hasOutputText := false
	content.ForEach(func(_, contentItem gjson.Result) bool {
		contentType := contentItem.Get("type").String()
		if contentType == "" {
			contentType = "input_text"
		}
		if contentType != "output_text" {
			return true
		}
		hasOutputText = true
		textParts = append(textParts, contentItem.Get("text").String())
		return true
	})
	if !hasOutputText {
		return "", false
	}
	// output_text marks model-visible content even when message.role is "user".
	return strings.Join(textParts, "\n"), true
}

func buildOpenAIResponsesReasoningModelContent(thoughtText, visibleText, signature string, useGeminiNativeReasoningLayout bool) []byte {
	modelContent := []byte(`{"role":"model","parts":[]}`)
	if useGeminiNativeReasoningLayout {
		thought := []byte(`{"text":"","thought":true}`)
		thought, _ = sjson.SetBytes(thought, "text", thoughtText)
		modelContent, _ = sjson.SetRawBytes(modelContent, "parts.-1", thought)

		visible := []byte(`{"text":"","thoughtSignature":""}`)
		visible, _ = sjson.SetBytes(visible, "text", visibleText)
		visible, _ = sjson.SetBytes(visible, "thoughtSignature", signature)
		modelContent, _ = sjson.SetRawBytes(modelContent, "parts.-1", visible)
		return modelContent
	}

	thought := []byte(`{"text":"","thoughtSignature":"","thought":true}`)
	thought, _ = sjson.SetBytes(thought, "text", thoughtText)
	thought, _ = sjson.SetBytes(thought, "thoughtSignature", signature)
	modelContent, _ = sjson.SetRawBytes(modelContent, "parts.-1", thought)
	return modelContent
}

func openAIResponsesGeminiThoughtSignature(rawSignature string) string {
	return sigcompat.GeminiReplaySignatureOrBypass(rawSignature, sigcompat.SignatureBlockKindGeminiModelPart)
}

func applyOpenAIResponsesTextFormatToGemini(out []byte, root gjson.Result) []byte {
	textFormat := root.Get("text.format")
	if !textFormat.Exists() {
		return out
	}

	formatType := strings.ToLower(strings.TrimSpace(textFormat.Get("type").String()))
	switch formatType {
	case "json_object":
		out = ensureGeminiGenerationConfig(out)
		out, _ = sjson.SetBytes(out, "generationConfig.responseMimeType", "application/json")
	case "json_schema":
		out = ensureGeminiGenerationConfig(out)
		out, _ = sjson.SetBytes(out, "generationConfig.responseMimeType", "application/json")
		out, _ = sjson.DeleteBytes(out, "generationConfig.responseSchema")

		schema := textFormat.Get("schema")
		if !schema.Exists() {
			schema = textFormat.Get("json_schema.schema")
		}
		if schema.Exists() {
			out, _ = sjson.SetRawBytes(out, "generationConfig.responseJsonSchema", []byte(schema.Raw))
		}
	}

	return out
}

func ensureGeminiGenerationConfig(out []byte) []byte {
	if !gjson.GetBytes(out, "generationConfig").Exists() {
		out, _ = sjson.SetRawBytes(out, "generationConfig", []byte(`{}`))
	}
	return out
}

type responsesToolMeta struct {
	OriginalName string
	Namespace    string
	IsCustom     bool
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

type openAIResponsesToolDef struct {
	Type        string
	Name        string
	Namespace   string
	Description string
	Parameters  string
}

func extractAllOpenAIResponsesTools(root gjson.Result) []openAIResponsesToolDef {
	var results []openAIResponsesToolDef

	var processTool func(tool gjson.Result, ns string)
	processTool = func(tool gjson.Result, ns string) {
		toolType := tool.Get("type").String()
		if toolType == "namespace" {
			currentNs := strings.TrimSpace(tool.Get("name").String())
			if currentNs == "" {
				currentNs = ns
			}
			if children := tool.Get("tools"); children.Exists() && children.IsArray() {
				children.ForEach(func(_, child gjson.Result) bool {
					processTool(child, currentNs)
					return true
				})
			}
			return
		}

		name := strings.TrimSpace(tool.Get("name").String())
		if name == "" {
			return
		}

		desc := tool.Get("description").String()
		params := tool.Get("parameters").Raw

		if toolType == "" {
			toolType = "function"
		}

		results = append(results, openAIResponsesToolDef{
			Type:        toolType,
			Name:        name,
			Namespace:   ns,
			Description: desc,
			Parameters:  params,
		})
	}

	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			processTool(tool, "")
			return true
		})
	}

	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				if tools := item.Get("tools"); tools.Exists() && tools.IsArray() {
					tools.ForEach(func(_, tool gjson.Result) bool {
						processTool(tool, "")
						return true
					})
				}
			}
			return true
		})
	}

	return results
}

func parseResponsesToolMetaMap(reqJSON []byte) map[string]responsesToolMeta {
	if len(reqJSON) == 0 || !gjson.ValidBytes(reqJSON) {
		return nil
	}
	root := unwrapRequestRoot(gjson.ParseBytes(reqJSON))
	allTools := extractAllOpenAIResponsesTools(root)
	if len(allTools) == 0 {
		return nil
	}

	metaMap := make(map[string]responsesToolMeta)
	for _, tool := range allTools {
		fullName := namespacedToolName(tool.Namespace, tool.Name)
		sanitizedName := util.SanitizeFunctionName(fullName)
		isCustom := tool.Type == "custom"

		meta := responsesToolMeta{
			OriginalName: tool.Name,
			Namespace:    tool.Namespace,
			IsCustom:     isCustom,
		}

		metaMap[sanitizedName] = meta
		metaMap[fullName] = meta
		if tool.Namespace != "" {
			if _, exists := metaMap[tool.Name]; !exists {
				metaMap[tool.Name] = meta
			}
			sanitizedOrig := util.SanitizeFunctionName(tool.Name)
			if _, exists := metaMap[sanitizedOrig]; !exists {
				metaMap[sanitizedOrig] = meta
			}
		}
	}
	return metaMap
}
