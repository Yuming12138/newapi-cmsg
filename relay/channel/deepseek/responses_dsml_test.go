package deepseek

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertDSMLTextCustomTool(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})
	text := `before<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="exec">
<｜｜DSML｜｜parameter name="input" string="true">const answer = 42;
text(answer);</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

	result := convertDSMLText(text, toolMap)

	require.Equal(t, "before", result.Text)
	require.Len(t, result.Tools, 1)
	tool := result.Tools[0]
	require.Equal(t, "custom_tool_call", tool.Type)
	require.Equal(t, "exec", tool.Name)
	require.Equal(t, "const answer = 42;\ntext(answer);", common.JsonRawMessageToString(tool.Input))
	require.NotEmpty(t, tool.CallId)
}

func TestConvertDSMLTextFunctionParametersAndMultipleInvokes(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{
		{"type": "function", "name": "lookup"},
		{"type": "function", "name": "notify"},
	})
	text := `<|DSML|function_calls><|DSML|invoke name='lookup'><|DSML|parameter name='query' string='true'>weather</|DSML|parameter><|DSML|parameter name='limit' string='false'>3</|DSML|parameter></|DSML|invoke><|DSML|invoke name='notify'><|DSML|parameter name='urgent' string='false'>true</|DSML|parameter></|DSML|invoke></|DSML|function_calls>`

	result := convertDSMLText(text, toolMap)

	require.Empty(t, result.Text)
	require.Len(t, result.Tools, 2)
	require.Equal(t, "lookup", result.Tools[0].Name)
	require.JSONEq(t, `{"query":"weather","limit":3}`, result.Tools[0].ArgumentsString())
	require.Equal(t, "notify", result.Tools[1].Name)
	require.JSONEq(t, `{"urgent":true}`, result.Tools[1].ArgumentsString())
}

func TestConvertDSMLTextNamespaceFunction(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{
		"type":  "namespace",
		"name":  "browser",
		"tools": []any{map[string]any{"type": "function", "name": "open"}},
	}})
	text := `<｜DSML｜tool_calls><｜DSML｜invoke name="browser__open"><｜DSML｜parameter name="url" string="true">https://example.com</｜DSML｜parameter></｜DSML｜invoke></｜DSML｜tool_calls>`

	result := convertDSMLText(text, toolMap)

	require.Len(t, result.Tools, 1)
	require.Equal(t, "function_call", result.Tools[0].Type)
	require.Equal(t, "open", result.Tools[0].Name)
	require.Equal(t, "browser", result.Tools[0].Namespace)
}

func TestConvertDSMLTextFailsOpen(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})
	cases := []string{
		"ordinary text mentioning DSML",
		`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">partial`,
		`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="unknown"><｜｜DSML｜｜parameter name="input" string="true">x</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`,
	}
	for _, input := range cases {
		result := convertDSMLText(input, toolMap)
		require.Equal(t, input, result.Text)
		require.Empty(t, result.Tools)
	}
}

func TestNativeResponsesStreamBuffersDSMLStartSplitAcrossChunks(t *testing.T) {
	state := &nativeResponsesStreamState{toolMap: buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})}
	consumed, output := state.consumeTextDelta("plain <｜｜DS")
	require.True(t, consumed)
	require.Equal(t, "plain ", output)

	consumed, output = state.consumeTextDelta(`ML｜｜tool_calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">x</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`)
	require.True(t, consumed)
	require.Empty(t, output)
	require.True(t, state.buffering)
	require.Contains(t, state.buffer.String(), "tool_calls")
}

func TestConvertNativeResponsesOutputPreservesMessageOrder(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})
	dsml := `<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">text("ok")</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`
	outputs := []dto.ResponsesOutput{{
		Type: "message", ID: "msg_1", Role: "assistant", Status: "completed",
		Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "I will run it.\n" + dsml}},
	}}

	converted, changed := convertNativeResponsesOutput(outputs, toolMap)

	require.True(t, changed)
	require.Len(t, converted, 2)
	require.Equal(t, "message", converted[0].Type)
	require.Equal(t, "I will run it.", converted[0].Content[0].Text)
	require.Equal(t, "custom_tool_call", converted[1].Type)
}

func TestNativeResponsesStreamConvertsSplitDSMLAndCompletedOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setNativeResponsesToolMap(c, []map[string]any{{"type": "custom", "name": "exec"}})
	dsml := `<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">text("ok")</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`
	parts := []string{
		`<｜｜DSML｜｜tool_`,
		`calls><｜｜DSML｜｜invoke name="exec"><｜｜DSML｜｜parameter name="input" string="true">`,
		`text("ok")</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`,
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_1"}},
		{"type": "response.output_text.delta", "delta": "prefix " + parts[0]},
		{"type": "response.output_text.delta", "delta": parts[1]},
		{"type": "response.output_text.delta", "delta": parts[2]},
		{"type": "response.output_text.done", "text": "prefix " + dsml},
		{"type": "response.completed", "response": map[string]any{
			"id": "resp_1", "status": "completed",
			"output": []any{map[string]any{
				"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": "prefix " + dsml}},
			}},
			"usage": map[string]any{"input_tokens": 4, "output_tokens": 5, "total_tokens": 9},
		}},
	}
	var upstream bytes.Buffer
	for _, event := range events {
		data, err := common.Marshal(event)
		require.NoError(t, err)
		upstream.WriteString("data: ")
		upstream.Write(data)
		upstream.WriteString("\n\n")
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(upstream.String()))}
	info := testRelayInfo("deepseek-v4-pro")
	info.IsStream = true

	usage, apiErr := handleNativeResponsesStream(c, resp, info)

	require.Nil(t, apiErr)
	require.Equal(t, 4, usage.PromptTokens)
	require.Equal(t, 5, usage.CompletionTokens)
	body := recorder.Body.String()
	require.Contains(t, body, `event: response.custom_tool_call_input.delta`)
	require.Contains(t, body, `"type":"custom_tool_call"`)
	require.Contains(t, body, `"name":"exec"`)
	require.NotContains(t, body, `response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"<`)
	require.Contains(t, body, `"output":[{"type":"message"`)
	require.Contains(t, body, `"type":"custom_tool_call"`)
}
