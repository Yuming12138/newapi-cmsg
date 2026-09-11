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

func TestConvertDSMLTextCallsAliasWithSpacedMarker(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})
	text := `before< | | DSML | | calls>
< | | DSML | | invoke name="exec">
< | | DSML | | parameter name="input" string="true">inspect the current page</ | | DSML | | parameter>
</ | | DSML | | invoke>
</ | | DSML | | calls>`

	result := convertDSMLText(text, toolMap)

	require.Equal(t, "before", result.Text)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "custom_tool_call", result.Tools[0].Type)
	require.Equal(t, "exec", result.Tools[0].Name)
	require.Equal(t, "inspect the current page", common.JsonRawMessageToString(result.Tools[0].Input))
}

func TestConvertDSMLTextEscapedLineSeparators(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})
	text := "before<｜｜DSML｜｜ calls>\\\n" +
		"<｜｜DSML｜｜ invoke name=\"exec\">\\\n" +
		"<｜｜DSML｜｜ parameter name=\"input\" string=\"true\">const results = await Promise.all([\\\n" +
		"  tools.exec_command({cmd: \"Get-Item\"})\\\n" +
		"]);\\\n" +
		"</｜｜DSML｜｜ parameter>\\\n" +
		"</｜｜DSML｜｜ invoke>\\\n" +
		"</｜｜DSML｜｜ calls>"

	result := convertDSMLText(text, toolMap)

	require.Equal(t, "before", result.Text)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "const results = await Promise.all([\n  tools.exec_command({cmd: \"Get-Item\"})\n]);\n", common.JsonRawMessageToString(result.Tools[0].Input))
}

func TestConvertDSMLTextScreenshotPayload(t *testing.T) {
	toolMap := buildNativeResponsesToolMap([]map[string]any{{
		"type": "additional_tools",
		"tools": []any{map[string]any{
			"type":  "namespace",
			"name":  "container",
			"tools": []any{map[string]any{"type": "custom", "name": "exec"}},
		}},
	}})
	text := `理解，这个 correction 很关键。我先不生成图，先看现有网页里“展示图”会落在哪个位置、当前素材尺寸和文字压图关系，再给具体建议。之前的图形探索里，适合展示图的方向和适合 Logo 的方向应该分开看待。
<｜｜DSML｜｜ calls>\
<｜｜DSML｜｜ invoke name="exec">\
<｜｜DSML｜｜ parameter name="input" string="true">const r = await tools.exec_command({cmd:"$p=Join-Path $env:TEMP 'cmsg-home-2.html'; C:\Windows\System32\curl.exe -L -sS --max-time 25 '[https://www.comates.group/](https://www.comates.group/)' -o $p; $html=Get-Content -LiteralPath $p -Raw; 'IMAGES:'; [regex]::Matches($html,'\<img[^>]+src="([^"]+)"','IgnoreCase') | ForEach-Object { $*.Groups[1].Value } | Select-Object -Unique; 'SECTIONS:'; [regex]::Matches($html,'\<h[12][^>]>(.?)\</h[12]>','IgnoreCase,Singleline') | ForEach-Object { ($*.Groups[1].Value -replace '<[^>]+>','' -replace '\s+',' ').Trim() } | Where-Object { $\_ } | Select-Object -First 30",yield_time_ms:30000,max_output_tokens:6000}); text(r.output);\
</｜｜DSML｜｜ parameter>\
</｜｜DSML｜｜ invoke>\
</｜｜DSML｜｜ calls>`

	result := convertDSMLText(text, toolMap)

	require.Equal(t, "理解，这个 correction 很关键。我先不生成图，先看现有网页里“展示图”会落在哪个位置、当前素材尺寸和文字压图关系，再给具体建议。之前的图形探索里，适合展示图的方向和适合 Logo 的方向应该分开看待。", result.Text)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "custom_tool_call", result.Tools[0].Type)
	require.Equal(t, "exec", result.Tools[0].Name)
	require.Contains(t, common.JsonRawMessageToString(result.Tools[0].Input), "tools.exec_command")
}

func TestConvertDSMLTextInfersUnknownCustomTool(t *testing.T) {
	text := `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="exec"><｜｜DSML｜｜ parameter name="input" string="true">text("ok")</｜｜DSML｜｜ parameter></｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`

	result := convertDSMLText(text, nil)

	require.Empty(t, result.Text)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "custom_tool_call", result.Tools[0].Type)
	require.Equal(t, "exec", result.Tools[0].Name)
	require.Equal(t, `text("ok")`, common.JsonRawMessageToString(result.Tools[0].Input))
}

func TestSetNativeResponsesToolMapForRequestIncludesAdditionalToolsInput(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Input: []byte(`[{"type":"additional_tools","tools":[{"type":"namespace","name":"container","tools":[{"type":"custom","name":"exec"}]}]}]`),
	}
	c, _ := gin.CreateTestContext(nil)
	setNativeResponsesToolMapForRequest(c, request)

	toolMap := getNativeResponsesToolMap(c)
	require.Contains(t, toolMap, "exec")
	require.Equal(t, "custom", toolMap["exec"].Type)
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

func TestNativeResponsesStreamRecognizesSpacedCallsAlias(t *testing.T) {
	state := &nativeResponsesStreamState{toolMap: buildNativeResponsesToolMap([]map[string]any{{"type": "custom", "name": "exec"}})}
	consumed, output := state.consumeTextDelta("plain< | | DSML | | calls>")

	require.True(t, consumed)
	require.Equal(t, "plain", output)
	require.True(t, state.buffering)
	require.Contains(t, state.buffer.String(), "calls")
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
	dsml := `< | | DSML | | calls>< | | DSML | | invoke name="exec">< | | DSML | | parameter name="input" string="true">text("ok")</ | | DSML | | parameter></ | | DSML | | invoke></ | | DSML | | calls>`
	parts := []string{
		`< | | DSML | | ca`,
		`lls>< | | DSML | | invoke name="exec">< | | DSML | | parameter name="input" string="true">`,
		`text("ok")</ | | DSML | | parameter></ | | DSML | | invoke></ | | DSML | | calls>`,
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

func TestNativeResponsesStreamConvertsOutputTextDoneDSML(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setNativeResponsesToolMap(c, []map[string]any{{"type": "custom", "name": "exec"}})
	dsml := `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="exec"><｜｜DSML｜｜ parameter name="input" string="true">text("ok")</｜｜DSML｜｜ parameter></｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_2"}},
		{"type": "response.output_text.done", "text": "prefix " + dsml},
		{"type": "response.completed", "response": map[string]any{
			"id": "resp_2", "status": "completed",
			"output": []any{map[string]any{
				"type": "message", "id": "msg_2", "role": "assistant", "status": "completed",
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
	info := testRelayInfo("deepseek-v4-flash")
	info.IsStream = true

	usage, apiErr := handleNativeResponsesStream(c, resp, info)

	require.Nil(t, apiErr)
	require.Equal(t, 4, usage.PromptTokens)
	require.Equal(t, 5, usage.CompletionTokens)
	body := recorder.Body.String()
	require.Contains(t, body, `event: response.custom_tool_call_input.delta`)
	require.Contains(t, body, `"type":"custom_tool_call"`)
	require.Contains(t, body, `"name":"exec"`)
	require.NotContains(t, body, `<｜｜DSML｜｜`)
}
