package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetModelRequestParsesPlaygroundImageEditMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("group", "cliproxy-codex"))
	require.NoError(t, writer.WriteField("prompt", "replace the background"))
	part, err := writer.CreateFormFile("image[]", "reference.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake image"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/images/edits", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() {
		common.CleanupBodyStorage(c)
	})

	modelRequest, shouldSelectChannel, err := getModelRequest(c)
	require.NoError(t, err)
	require.True(t, shouldSelectChannel)
	require.Equal(t, "gpt-image-2", modelRequest.Model)
	require.Equal(t, "cliproxy-codex", modelRequest.Group)
	require.Equal(t, "cliproxy-codex", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
}

func TestPathToRelayModeIncludesPlaygroundImages(t *testing.T) {
	require.Equal(t, relayconstant.RelayModeImagesGenerations, relayconstant.Path2RelayMode("/pg/images/generations"))
	require.Equal(t, relayconstant.RelayModeImagesEdits, relayconstant.Path2RelayMode("/pg/images/edits"))
}
