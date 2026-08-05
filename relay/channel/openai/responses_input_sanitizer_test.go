package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestNormalizesMaxReasoningSuffix(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-luna-max",
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.6-luna-max",
		},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted := convertedAny.(dto.OpenAIResponsesRequest)

	require.Equal(t, "gpt-5.6-luna", converted.Model)
	require.NotNil(t, converted.Reasoning)
	require.Equal(t, "max", converted.Reasoning.Effort)
	require.Equal(t, "gpt-5.6-luna", info.UpstreamModelName)
	require.Equal(t, "max", info.ReasoningEffort)
}

func TestConvertOpenAIResponsesRequestDropsForeignToolItemIDsForGPT5(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte("[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"user\",\"content\":\"hello\"},{\"type\":\"custom_tool_call\",\"id\":\"fc_foreign_custom\",\"call_id\":\"call_1\",\"name\":\"exec\",\"input\":\"{}\"},{\"type\":\"custom_tool_call_output\",\"id\":\"toolu_foreign_output\",\"call_id\":\"call_1\",\"output\":\"ok\"},{\"type\":\"function_call\",\"id\":\"fc_foreign_function\",\"call_id\":\"call_2\",\"name\":\"lookup\",\"arguments\":\"{}\"},{\"type\":\"custom_tool_call\",\"id\":\"ctc_valid\",\"call_id\":\"call_3\",\"name\":\"exec\",\"input\":\"{}\"},{\"type\":\"reasoning\",\"id\":\"rs_valid\",\"summary\":[]}]"),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
		},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted := convertedAny.(dto.OpenAIResponsesRequest)

	require.False(t, gjson.GetBytes(converted.Input, "1.id").Exists())
	require.False(t, gjson.GetBytes(converted.Input, "2.id").Exists())
	require.False(t, gjson.GetBytes(converted.Input, "3.id").Exists())
	require.Equal(t, "ctc_valid", gjson.GetBytes(converted.Input, "4.id").String())
	require.Equal(t, "rs_valid", gjson.GetBytes(converted.Input, "5.id").String())
	require.Equal(t, "call_1", gjson.GetBytes(converted.Input, "1.call_id").String())
}

func TestConvertOpenAIResponsesRequestDropsInvalidReasoningItemIDsForGPT5(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte(`[{"type":"reasoning","id":"item_4793a7d4b4169783769f3a36","summary":[]},{"type":"reasoning","id":"rs_valid","summary":[]}]`),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
		},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted := convertedAny.(dto.OpenAIResponsesRequest)

	require.False(t, gjson.GetBytes(converted.Input, "0.id").Exists())
	require.Equal(t, "rs_valid", gjson.GetBytes(converted.Input, "1.id").String())
	require.Equal(t, "reasoning", gjson.GetBytes(converted.Input, "0.type").String())
}

func TestConvertOpenAIResponsesRequestDropsInvalidMessageItemIDsForGPT5(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte(`[{"type":"message","id":"item_22d7f956efd39187f25a2e81","role":"assistant","content":[{"type":"output_text","text":"hello"}]},{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"hi"}]}]`),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
		},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted := convertedAny.(dto.OpenAIResponsesRequest)

	require.False(t, gjson.GetBytes(converted.Input, "0.id").Exists())
	require.Equal(t, "msg_valid", gjson.GetBytes(converted.Input, "1.id").String())
	require.Equal(t, "message", gjson.GetBytes(converted.Input, "0.type").String())
}

func TestConvertOpenAIResponsesRequestKeepsForeignToolItemIDsForNonGPT5(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte("[{\"type\":\"custom_tool_call\",\"id\":\"fc_foreign_custom\",\"call_id\":\"call_1\",\"name\":\"exec\",\"input\":\"{}\"},{\"type\":\"function_call\",\"id\":\"fc_foreign_function\",\"call_id\":\"call_2\",\"name\":\"lookup\",\"arguments\":\"{}\"}]"),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "grok-4.5",
		},
	}

	convertedAny, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	converted := convertedAny.(dto.OpenAIResponsesRequest)

	require.Equal(t, "fc_foreign_custom", gjson.GetBytes(converted.Input, "0.id").String())
	require.Equal(t, "fc_foreign_function", gjson.GetBytes(converted.Input, "1.id").String())
}
