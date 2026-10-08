package deepseek

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useNativeReasoningCache(t *testing.T) {
	t.Helper()
	oldEnabled, oldRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	nativeReasoningMemory().Purge()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = oldEnabled, oldRDB
		nativeReasoningMemory().Purge()
	})
}

func nativeReasoningTestInfo() *relaycommon.RelayInfo {
	info := testRelayInfo("deepseek-flash")
	info.UserId, info.TokenId, info.ChannelId = 2, 19, 11
	info.ApiKey = "synthetic-test-credential"
	info.RequestHeaders = map[string]string{"Thread-Id": "thread-one", "Session-Id": "session-one"}
	return info
}

func nativeReasoningTestOutput(id, text string) dto.ResponsesOutput {
	return dto.ResponsesOutput{Type: "reasoning", ID: id, Status: "completed",
		Content: []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: text}}}
}

func nativeReasoningTestCall(id string) dto.ResponsesOutput {
	return dto.ResponsesOutput{Type: "function_call", ID: "fc_" + id, CallId: id,
		Name: "lookup", Arguments: rawString(`{"query":"test"}`)}
}

func nativeReasoningTestRequest(t *testing.T, items ...map[string]any) dto.OpenAIResponsesRequest {
	t.Helper()
	data, err := common.Marshal(items)
	require.NoError(t, err)
	request := dto.OpenAIResponsesRequest{Model: "deepseek-flash", Input: data}
	require.NoError(t, common.Unmarshal([]byte(`{"effort":"high"}`), &request.Reasoning))
	return request
}

func nativeReasoningTestText(t *testing.T, item map[string]any) string {
	t.Helper()
	parts := mapSlice(item["content"])
	require.NotEmpty(t, parts)
	require.Equal(t, "reasoning_text", parts[0]["type"])
	return common.Interface2String(parts[0]["text"])
}

func TestRestoreNativeReasoningByOutputAnchors(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	original := "  original plaintext\n原始思考\t  "
	outputs := []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", original),
		{Type: "message", ID: "msg_1", Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "looking up"}}},
		nativeReasoningTestCall("call_1"),
	}
	require.NoError(t, rememberNativeResponsesReasoning(info, outputs))

	for _, tc := range []struct {
		name   string
		anchor map[string]any
	}{
		{"reasoning_id", map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{}, "summary": []any{map[string]any{"type": "summary_text", "text": "not the original"}}, "encrypted_content": "opaque"}},
		{"assistant_message_id", map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": "looking up"}},
		{"tool_call_id", map[string]any{"type": "custom_tool_call", "call_id": "call_1", "name": "lookup", "input": "test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := nativeReasoningTestRequest(t, tc.anchor)
			got, err := restoreNativeResponsesReasoning(info, request)
			require.NoError(t, err)
			require.Equal(t, request.Reasoning, got.Reasoning)
			items, err := normalizeResponsesInput(got.Input)
			require.NoError(t, err)
			require.Equal(t, "reasoning", items[0]["type"])
			require.Equal(t, original, nativeReasoningTestText(t, items[0]))
			require.NotContains(t, items[0], "encrypted_content")
			if tc.name == "reasoning_id" {
				require.Len(t, items, 1)
			} else {
				require.Len(t, items, 2)
				require.Equal(t, tc.anchor["type"], items[1]["type"])
			}
		})
	}
}

func TestRestoreNativeReasoningNeverPromotesForeignSummaryOrCiphertext(t *testing.T) {
	useNativeReasoningCache(t)
	request := nativeReasoningTestRequest(t,
		map[string]any{"type": "reasoning", "id": "rs_foreign", "content": []any{}, "summary": []any{map[string]any{"type": "summary_text", "text": "foreign summary"}}, "encrypted_content": "foreign-ciphertext"},
		map[string]any{"type": "function_call", "call_id": "call_foreign", "name": "lookup", "arguments": "{}"},
	)
	got, err := restoreNativeResponsesReasoning(nativeReasoningTestInfo(), request)
	require.NoError(t, err)
	require.Equal(t, request.Input, got.Input)
	require.Equal(t, request.Reasoning, got.Reasoning)
}

func TestRestoreNativeReasoningNormalizesTextWithoutChangingBytes(t *testing.T) {
	useNativeReasoningCache(t)
	original := "\n  actual plaintext\t原文  "
	request := nativeReasoningTestRequest(t, map[string]any{"type": "reasoning",
		"content": []any{map[string]any{"type": "text", "text": original}},
		"summary": []any{map[string]any{"type": "summary_text", "text": "different summary"}}})
	got, err := restoreNativeResponsesReasoning(nativeReasoningTestInfo(), request)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, original, nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningAdaptorStatelessToolContinuation(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "must replay this exact original"),
		{Type: "custom_tool_call", ID: "call_1", CallId: "call_1", Name: "exec", Input: rawString("text(1)")},
	}))
	request := nativeReasoningTestRequest(t,
		map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{}, "encrypted_content": "not-replayable"},
		map[string]any{"type": "custom_tool_call", "call_id": "call_1", "name": "exec", "input": "text(1)"},
		map[string]any{"role": "assistant", "content": "running the tool"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call_1", "output": "1"},
	)
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	got := converted.(dto.OpenAIResponsesRequest)
	require.Equal(t, request.Reasoning, got.Reasoning)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, "must replay this exact original", nativeReasoningTestText(t, items[0]))
	require.Equal(t, "function_call", items[1]["type"])
	require.JSONEq(t, `{"input":"text(1)"}`, items[1]["arguments"].(string))
	require.Equal(t, "function_call_output", items[2]["type"])
	require.Equal(t, "call_1", items[2]["call_id"])
	require.Equal(t, "assistant", items[3]["role"])
}

func TestNativeReasoningCacheScopeIsolation(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "private original"), nativeReasoningTestCall("call_1"),
	}))
	request := nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": "{}"})
	for _, tc := range []struct {
		name       string
		change     func(*relaycommon.RelayInfo)
		expectSame bool
	}{
		{"user", func(i *relaycommon.RelayInfo) { i.UserId++ }, false},
		{"token", func(i *relaycommon.RelayInfo) { i.TokenId++ }, false},
		{"channel", func(i *relaycommon.RelayInfo) { i.ChannelId++ }, false},
		{"model", func(i *relaycommon.RelayInfo) { i.UpstreamModelName = "deepseek-v4-pro" }, false},
		{"destination", func(i *relaycommon.RelayInfo) { i.ChannelBaseUrl = "https://other.invalid" }, false},
		{"credential", func(i *relaycommon.RelayInfo) { i.ApiKey = "different-synthetic-credential" }, false},
		{"thread", func(i *relaycommon.RelayInfo) {
			i.RequestHeaders = map[string]string{"thread-id": "thread-two", "session-id": "session-one"}
		}, false},
		{"session_does_not_override_thread", func(i *relaycommon.RelayInfo) { i.RequestHeaders["Session-Id"] = "session-two" }, true},
		{"unknown_thread", func(i *relaycommon.RelayInfo) { i.RequestHeaders = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := nativeReasoningTestInfo()
			tc.change(other)
			got, err := restoreNativeResponsesReasoning(other, request)
			require.NoError(t, err)
			if tc.expectSame {
				require.NotEqual(t, request.Input, got.Input)
			} else {
				require.Equal(t, request.Input, got.Input)
			}
		})
	}
}

func TestNativeReasoningInvalidCacheDoesNotDestroyCallerHistory(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	request := nativeReasoningTestRequest(t, map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{map[string]any{"type": "reasoning_text", "text": "caller's original"}}})
	invalid, err := marshalNativeReasoningRecord([]map[string]any{{"type": "message", "content": "not reasoning"}})
	require.NoError(t, err)
	expired, err := common.Marshal(nativeReasoningRecord{Items: []map[string]any{{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": "expired"}}}}, ExpiresAt: time.Now().Add(-time.Second).UnixMilli()})
	require.NoError(t, err)
	for _, raw := range []string{"{invalid", string(invalid), string(expired)} {
		nativeReasoningMemory().SetWithTTL(nativeReasoningKey(info, "reasoning|rs_1"), raw, nativeReasoningTTL)
		got, err := restoreNativeResponsesReasoning(info, request)
		require.NoError(t, err)
		require.Equal(t, request.Input, got.Input)
	}
}

func TestNativeReasoningDedupAndSequentialGroups(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "first"), nativeReasoningTestCall("call_1"), nativeReasoningTestCall("call_2"),
		nativeReasoningTestOutput("rs_2", "second"), nativeReasoningTestCall("call_3"),
	}))
	request := nativeReasoningTestRequest(t,
		map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{}},
		map[string]any{"type": "function_call", "call_id": "call_1"},
		map[string]any{"type": "function_call", "call_id": "call_2"},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		map[string]any{"type": "function_call", "call_id": "call_3"},
	)
	got, err := restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 6)
	require.Equal(t, "first", nativeReasoningTestText(t, items[0]))
	require.Equal(t, "second", nativeReasoningTestText(t, items[4]))
}

func TestNativeReasoningNoIDRecoveredAndDeduplicatedByCalls(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("", "no-id original"), nativeReasoningTestCall("call_1"), nativeReasoningTestCall("call_2"),
	}))
	request := nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1"}, map[string]any{"type": "function_call", "call_id": "call_2"})
	got, err := restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "no-id original", nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningParallelCallsStayDeduplicatedAcrossToolResults(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "one parallel reasoning group"), nativeReasoningTestCall("call_1"), nativeReasoningTestCall("call_2"),
	}))
	request := nativeReasoningTestRequest(t,
		map[string]any{"type": "function_call", "call_id": "call_1"},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "first"},
		map[string]any{"type": "function_call", "call_id": "call_2"},
		map[string]any{"type": "function_call_output", "call_id": "call_2", "output": "second"},
	)
	got, err := restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 5)
	require.Equal(t, "one parallel reasoning group", nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningConcurrentMemoryAccess(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	request := nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1"})
	var workers sync.WaitGroup
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for attempt := 0; attempt < 8; attempt++ {
				if err := rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{nativeReasoningTestOutput("rs_1", "concurrent original"), nativeReasoningTestCall("call_1")}); err != nil {
					t.Error(err)
					return
				}
				got, err := restoreNativeResponsesReasoning(info, request)
				if err != nil {
					t.Error(err)
					return
				}
				items, err := normalizeResponsesInput(got.Input)
				if err != nil || len(items) != 2 || common.Interface2String(items[0]["type"]) != "reasoning" {
					t.Error("concurrent cache access did not restore reasoning")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestNativeReasoningRedisSurvivesMemoryReloadAndExpires(t *testing.T) {
	useNativeReasoningCache(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RedisEnabled, common.RDB = true, client
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "survives reload"), nativeReasoningTestCall("call_1"),
	}))
	key := nativeReasoningKey(info, "call|call_1")
	require.Equal(t, nativeReasoningTTL, server.TTL(key))
	nativeReasoningMemory().Purge()
	request := nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1"})
	got, err := restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "survives reload", nativeReasoningTestText(t, items[0]))
	nativeReasoningMemory().Purge()
	server.FastForward(nativeReasoningTTL + time.Second)
	got, err = restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	require.Equal(t, request.Input, got.Input)
}

func TestNativeReasoningCacheSizeBounds(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	require.NoError(t, rememberNativeResponsesReasoning(info, []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_huge", strings.Repeat("x", nativeReasoningMaxBytes+1)), nativeReasoningTestCall("call_huge"),
	}))
	request := nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_huge"})
	got, err := restoreNativeResponsesReasoning(info, request)
	require.NoError(t, err)
	require.Equal(t, request.Input, got.Input)
	for index := 0; index <= nativeReasoningCapacity; index++ {
		key := "bounded-key-" + string(rune('A'+index))
		require.NoError(t, storeNativeReasoningRecords(map[string]string{key: "bounded"}))
	}
	_, found, _ := nativeReasoningMemory().Get("bounded-key-A")
	require.False(t, found)
}

func nativeReasoningTestStream(t *testing.T, info *relaycommon.RelayInfo, events []map[string]any, tools []map[string]any) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setNativeResponsesToolMap(c, tools)
	var upstream bytes.Buffer
	for _, event := range events {
		data, err := common.Marshal(event)
		require.NoError(t, err)
		upstream.WriteString("data: ")
		upstream.Write(data)
		upstream.WriteString("\n\n")
	}
	info.IsStream = true
	_, apiErr := handleNativeResponsesStream(c, &http.Response{StatusCode: 200, Body: io.NopCloser(&upstream)}, info)
	require.Nil(t, apiErr)
	return recorder.Body.String()
}

func TestNativeReasoningStreamRestoresBeforeResponseCompleted(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	body := nativeReasoningTestStream(t, info, []map[string]any{
		{"type": "response.output_item.done", "item": nativeReasoningTestOutput("rs_1", "original before interruption")},
		{"type": "response.output_item.done", "item": nativeReasoningTestCall("call_1")},
	}, nil)
	require.NotContains(t, body, "response.completed")
	got, err := restoreNativeResponsesReasoning(info, nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1"}))
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "original before interruption", nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningStreamUsesTextDoneWhenItemHasNoContent(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	nativeReasoningTestStream(t, info, []map[string]any{
		{"type": "response.reasoning_text.done", "item_id": "rs_1", "content_index": 0, "text": "part one\n"},
		{"type": "response.reasoning_text.done", "item_id": "rs_1", "content_index": 1, "text": "part two"},
		{"type": "response.output_item.done", "item": dto.ResponsesOutput{Type: "reasoning", ID: "rs_1"}},
		{"type": "response.output_item.done", "item": nativeReasoningTestCall("call_1")},
	}, nil)
	got, err := restoreNativeResponsesReasoning(info, nativeReasoningTestRequest(t, map[string]any{"type": "function_call", "call_id": "call_1"}))
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	parts := mapSlice(items[0]["content"])
	require.Len(t, parts, 2)
	require.Equal(t, "part one\n", parts[0]["text"])
	require.Equal(t, "part two", parts[1]["text"])
}

func TestNativeReasoningDSMLCallAnchorSurvivesInterruptedStream(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	dsml := `<｜｜DSML｜｜calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">text(1)</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜calls>`
	body := nativeReasoningTestStream(t, info, []map[string]any{
		{"type": "response.output_item.done", "item": nativeReasoningTestOutput("rs_1", "reason for generated tool")},
		{"type": "response.output_text.done", "text": dsml},
	}, []map[string]any{{"type": "custom", "name": "exec"}})
	var callID string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event dto.ResponsesStreamResponse
		if common.UnmarshalJsonStr(strings.TrimPrefix(line, "data: "), &event) == nil && event.Item != nil && event.Item.Type == "custom_tool_call" {
			callID = event.Item.CallId
		}
	}
	require.NotEmpty(t, callID)
	got, err := restoreNativeResponsesReasoning(info, nativeReasoningTestRequest(t, map[string]any{"type": "custom_tool_call", "call_id": callID, "name": "exec", "input": "text(1)"}))
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "reason for generated tool", nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningNonStreamPreservesOriginal(t *testing.T) {
	useNativeReasoningCache(t)
	info := nativeReasoningTestInfo()
	response := dto.OpenAIResponsesResponse{ID: "resp_1", Output: []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_1", "nonstream original"), nativeReasoningTestCall("call_1"),
	}}
	data, err := common.Marshal(response)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, apiErr := handleNativeResponsesResponse(c, &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, info)
	require.Nil(t, apiErr)
	got, err := restoreNativeResponsesReasoning(info, nativeReasoningTestRequest(t, map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{}}))
	require.NoError(t, err)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Equal(t, "nonstream original", nativeReasoningTestText(t, items[0]))
}

func TestNativeReasoningPreviousResponseDatabaseReplay(t *testing.T) {
	useNativeReasoningCache(t)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.ResponsesChatSession{}))
	info := nativeReasoningTestInfo()
	request := nativeReasoningTestRequest(t, map[string]any{"role": "user", "content": "look this up"})
	response := &dto.OpenAIResponsesResponse{ID: "resp_db_1", Output: []dto.ResponsesOutput{
		nativeReasoningTestOutput("rs_db_1", "database original"), nativeReasoningTestCall("call_db_1"),
	}}
	require.NoError(t, commitNativeResponsesSession(info, request, response))
	next := nativeReasoningTestRequest(t, map[string]any{"type": "function_call_output", "call_id": "call_db_1", "output": "ok"})
	next.PreviousResponseID = response.ID
	got, err := prepareNativeResponsesRequest(info, next)
	require.NoError(t, err)
	require.Empty(t, got.PreviousResponseID)
	items, err := normalizeResponsesInput(got.Input)
	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, "database original", nativeReasoningTestText(t, items[1]))
}
