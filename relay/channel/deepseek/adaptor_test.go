package deepseek

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
)

func testRelayInfo(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: model,
		},
	}
}

func TestGetRequestURLUsesNativeResponsesEndpoint(t *testing.T) {
	info := testRelayInfo("deepseek-v4-flash")
	info.RelayMode = relayconstant.RelayModeResponses

	url, err := (&Adaptor{}).GetRequestURL(info)

	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/responses", url)
}

func TestProResponsesUsesNativeEndpoint(t *testing.T) {
	info := testRelayInfo("deepseek-v4-pro")
	info.RelayMode = relayconstant.RelayModeResponses
	stream := true
	request := dto.OpenAIResponsesRequest{
		Model:  "deepseek-v4-pro",
		Input:  json.RawMessage(`"use the new pro model"`),
		Stream: &stream,
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)

	require.NoError(t, err)
	payload, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, "deepseek-v4-pro", payload.Model)
	require.JSONEq(t, string(request.Input), string(payload.Input))
	require.Equal(t, request.Stream, payload.Stream)

	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/responses", url)
}

func TestConvertOpenAIResponsesRequestPreservesNativePayload(t *testing.T) {
	info := testRelayInfo("deepseek-v4-flash-max")
	stream := true
	maxOutputTokens := uint(4096)
	request := dto.OpenAIResponsesRequest{
		Model:           "gpt-5.6-sol",
		Input:           json.RawMessage(`"inspect the repository"`),
		Instructions:    json.RawMessage(`"be concise"`),
		MaxOutputTokens: &maxOutputTokens,
		Stream:          &stream,
		Tools: json.RawMessage(`[
			{"type":"function","name":"read_file","parameters":{"type":"object"}},
			{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}
		]`),
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)

	require.NoError(t, err)
	got, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, "deepseek-v4-flash", got.Model)
	require.JSONEq(t, string(request.Input), string(got.Input))
	require.JSONEq(t, string(request.Instructions), string(got.Instructions))
	require.JSONEq(t, string(request.Tools), string(got.Tools))
	require.Equal(t, request.MaxOutputTokens, got.MaxOutputTokens)
	require.Equal(t, request.Stream, got.Stream)
	require.NotNil(t, got.Reasoning)
	require.Equal(t, "max", got.Reasoning.Effort)
	require.Equal(t, "deepseek-v4-flash", info.UpstreamModelName)
	require.Equal(t, "max", info.ReasoningEffort)
}

func TestConvertOpenAIResponsesRequestMapsNoneSuffixToNoneEffort(t *testing.T) {
	info := testRelayInfo("deepseek-v4-flash-none")
	request := dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol"}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)

	require.NoError(t, err)
	got := converted.(dto.OpenAIResponsesRequest)
	require.Equal(t, "deepseek-v4-flash", got.Model)
	require.NotNil(t, got.Reasoning)
	require.Equal(t, "none", got.Reasoning.Effort)
	require.Equal(t, "none", info.ReasoningEffort)
}

func TestConvertOpenAIResponsesRequestKeepsExplicitReasoningEffort(t *testing.T) {
	info := testRelayInfo("deepseek-v4-flash")
	request := dto.OpenAIResponsesRequest{
		Model:     "deepseek-v4-flash",
		Reasoning: &dto.Reasoning{Effort: "high"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)

	require.NoError(t, err)
	got := converted.(dto.OpenAIResponsesRequest)
	require.Equal(t, "deepseek-v4-flash", got.Model)
	require.Equal(t, "high", got.Reasoning.Effort)
	require.Equal(t, "high", info.ReasoningEffort)
}
