package mimo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct{}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := strings.TrimRight(info.ChannelBaseUrl, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions", nil
	}
	return baseURL + "/v1/chat/completions", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Authorization", "Bearer "+info.ApiKey)
	header.Set("Content-Type", "application/json")
	if info.RelayMode == relayconstant.RelayModeResponses {
		header.Set("Accept", "application/json")
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	request.Model = info.UpstreamModelName
	payload := request.ToMap()
	if _, ok := payload["max_completion_tokens"]; !ok {
		if maxTokens, ok := payload["max_tokens"]; ok {
			payload["max_completion_tokens"] = maxTokens
		}
	}
	return filterChatPayload(payload), nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return convertResponsesRequest(c, info, request)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, fmt.Errorf("mimo does not support rerank")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, fmt.Errorf("mimo does not support embeddings")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, fmt.Errorf("mimo does not support audio relay")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, fmt.Errorf("mimo does not support image generation")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return nil, fmt.Errorf("mimo does not support claude request conversion")
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, fmt.Errorf("mimo does not support gemini request conversion")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeResponses {
		return handleResponsesChatResponse(c, resp, info)
	}
	if info.IsStream {
		return openai.OaiStreamHandler(c, info, resp)
	}
	return openai.OpenaiHandler(c, info, resp)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

var chatRequestParams = map[string]bool{
	"model":                 true,
	"messages":              true,
	"stream":                true,
	"temperature":           true,
	"top_p":                 true,
	"max_completion_tokens": true,
	"frequency_penalty":     true,
	"presence_penalty":      true,
	"response_format":       true,
	"stop":                  true,
	"tools":                 true,
	"tool_choice":           true,
	"thinking":              true,
}

func filterChatPayload(payload map[string]any) map[string]any {
	filtered := make(map[string]any)
	for key, value := range payload {
		if chatRequestParams[key] {
			filtered[key] = value
		}
	}
	if _, ok := filtered["thinking"]; !ok {
		if thinking := thinkingFromPayload(payload); thinking != nil {
			filtered["thinking"] = thinking
		}
	}
	if toolChoice, ok := filtered["tool_choice"]; ok && common.Interface2String(toolChoice) != "auto" {
		delete(filtered, "tool_choice")
	}
	return filtered
}

func thinkingFromPayload(payload map[string]any) map[string]string {
	effort := common.Interface2String(payload["reasoning_effort"])
	if effort == "" {
		if reasoning, ok := payload["reasoning"].(map[string]any); ok {
			effort = common.Interface2String(reasoning["effort"])
		}
	}
	if effort == "" {
		return nil
	}
	if effort == "none" || effort == "minimal" {
		return map[string]string{"type": "disabled"}
	}
	return map[string]string{"type": "enabled"}
}
