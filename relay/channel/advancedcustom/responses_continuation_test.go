package advancedcustom

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResponsesHistorySurvivesSwitchBetweenC2AAndASXS(t *testing.T) {
	t.Setenv("RESPONSES_SESSION_STORAGE_MODE", "incremental")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "responses.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.ResponsesChatSession{}, &model.ResponsesChatTurn{},
		&model.ResponsesChatHead{}, &model.ResponsesChatCheckpoint{}))

	chatInfo := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
		IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions",
		Converter: dto.AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
	}}})
	chatInfo.RelayMode = relayconstant.RelayModeResponses
	chatInfo.UserId, chatInfo.TokenId = 13, 27
	chatInfo.RequestHeaders = map[string]string{"thread-id": "switch-regression"}
	chatInfo.UpstreamModelName = "deepseek-v4.1-flash"

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 15
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	asxs := &relaycommon.RelayInfo{
		UserId: chatInfo.UserId, TokenId: chatInfo.TokenId,
		RequestHeaders: chatInfo.RequestHeaders, RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 1, ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://api.hfjtc.cfd",
			UpstreamModelName: "gpt-5.6-sol", ApiKey: "test-native-credential",
		},
	}
	wire := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(wire)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "first-chat-response")
	chat := &Adaptor{}
	converted, err := chat.ConvertOpenAIResponsesRequest(c, chatInfo, dto.OpenAIResponsesRequest{
		Model: chatInfo.UpstreamModelName, Instructions: []byte(`"keep the notes"`),
		Input: []byte(`[
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"foreign thinking"}]},
			{"type":"message","role":"user","content":"the reference is blue"}
		]`),
	})
	require.NoError(t, err)
	require.Len(t, converted.(*dto.GeneralOpenAIRequest).Messages, 2)
	chatInfo.IsStream = true
	_, apiErr := chat.DoResponse(c, continuationUpstreamStream(
		`{"id":"chat_1","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"private thinking","content":"I noted the reference"},"finish_reason":""}]}`,
		`{"id":"chat_1","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"check","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":6,"total_tokens":14}}`,
	), chatInfo)
	require.Nil(t, apiErr)
	chatID := "chatcmpl-first-chat-response"
	saved, err := model.GetResponsesChatSession(chatID)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, chatInfo.UserId, saved.UserId)
	require.Contains(t, wire.Body.String(), "response.completed")
	require.Contains(t, wire.Body.String(), chatID)

	c = advancedCustomGinContext("/v1/responses")
	native := &openai.Adaptor{}
	converted, err = native.ConvertOpenAIResponsesRequest(c, asxs, dto.OpenAIResponsesRequest{
		Model: asxs.UpstreamModelName, PreviousResponseID: chatID,
		Input: []byte(`[{"type":"function_call_output","call_id":"call_1","output":"reference verified"}]`),
	})
	require.NoError(t, err)
	replayed := converted.(dto.OpenAIResponsesRequest)
	require.Empty(t, replayed.PreviousResponseID)
	require.Contains(t, string(replayed.Input), "the reference is blue")
	require.Contains(t, string(replayed.Input), "I noted the reference")
	require.Contains(t, string(replayed.Input), "reference verified")
	require.NotContains(t, string(replayed.Input), "private thinking")
	require.NotContains(t, string(replayed.Input), `"content":null`)
	asxs.IsStream = true
	_, apiErr = native.DoResponse(c, continuationUpstreamStream(
		`{"type":"response.created","response":{"id":"resp_asxs_test","output":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_asxs_test","type":"message","role":"assistant","content":[{"type":"output_text","text":"confirmed blue"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_asxs_test","status":"completed","output":[],"usage":{"input_tokens":16,"output_tokens":3,"total_tokens":19}}}`,
	), asxs)
	require.Nil(t, apiErr)

	nativeFollowup := dto.OpenAIResponsesRequest{Model: asxs.UpstreamModelName, PreviousResponseID: "resp_asxs_test", Input: []byte(`"same model"`)}
	converted, err = (&openai.Adaptor{}).ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), asxs, nativeFollowup)
	require.NoError(t, err)
	require.Equal(t, "resp_asxs_test", converted.(dto.OpenAIResponsesRequest).PreviousResponseID)
	asxs.UpstreamModelName = "gpt-6.2-sol"
	nativeFollowup.Model = asxs.UpstreamModelName
	converted, err = (&openai.Adaptor{}).ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), asxs, nativeFollowup)
	require.NoError(t, err)
	require.Empty(t, converted.(dto.OpenAIResponsesRequest).PreviousResponseID)
	require.Contains(t, string(converted.(dto.OpenAIResponsesRequest).Input), "confirmed blue")

	c = advancedCustomGinContext("/v1/responses")
	converted, err = (&Adaptor{}).ConvertOpenAIResponsesRequest(c, chatInfo, dto.OpenAIResponsesRequest{
		Model: chatInfo.UpstreamModelName, PreviousResponseID: "resp_asxs_test", Input: []byte(`"what was the color?"`),
	})
	require.NoError(t, err)
	messages := converted.(*dto.GeneralOpenAIRequest).Messages
	require.Len(t, messages, 6)
	require.Equal(t, "the reference is blue", messages[1].StringContent())
	require.Equal(t, "call_1", messages[2].ParseToolCalls()[0].ID)
	require.Equal(t, "reference verified", messages[3].StringContent())
	require.Equal(t, "confirmed blue", messages[4].StringContent())
	require.Equal(t, "what was the color?", messages[5].StringContent())

	otherAccount := *chatInfo
	otherAccount.UserId++
	_, err = (&Adaptor{}).ConvertOpenAIResponsesRequest(c, &otherAccount, dto.OpenAIResponsesRequest{
		Model: chatInfo.UpstreamModelName, PreviousResponseID: "resp_asxs_test", Input: []byte(`"unrelated"`),
	})
	require.ErrorContains(t, err, "previous_response_id not found")

	c = advancedCustomGinContext("/v1/responses")
	c.Set(common.RequestIdKey, "no-store")
	noStore := &Adaptor{}
	chatInfo.IsStream = false
	_, err = noStore.ConvertOpenAIResponsesRequest(c, chatInfo, dto.OpenAIResponsesRequest{
		Model: chatInfo.UpstreamModelName, Store: []byte(`false`), Input: []byte(`"not retained"`),
	})
	require.NoError(t, err)
	_, apiErr = noStore.DoResponse(c, continuationUpstreamResponse(`{
		"model":"deepseek-v4.1-flash","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`), chatInfo)
	require.Nil(t, apiErr)
	noStoreSession, err := model.GetResponsesChatSession("chatcmpl-no-store")
	require.NoError(t, err)
	require.Nil(t, noStoreSession)
}

func continuationUpstreamResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func continuationUpstreamStream(events ...string) *http.Response {
	body := "data: " + strings.Join(events, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}
