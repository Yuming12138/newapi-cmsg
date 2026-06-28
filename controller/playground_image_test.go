package controller

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newPlaygroundImageMultipartContext(t *testing.T, fields map[string]string, images int, masks int) *gin.Context {
	t.Helper()

	gin.SetMode(gin.TestMode)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	for i := 0; i < images; i++ {
		part, err := writer.CreateFormFile("image[]", "reference.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fake image"))
		require.NoError(t, err)
	}
	for i := 0; i < masks; i++ {
		part, err := writer.CreateFormFile("mask", "mask.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fake mask"))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, playgroundImagesEdits, body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() {
		common.CleanupBodyStorage(c)
	})
	return c
}

func TestValidatePlaygroundImageEditMultipartInput(t *testing.T) {
	c := newPlaygroundImageMultipartContext(t, map[string]string{
		"model":         "gpt-image-2",
		"prompt":        "replace the background",
		"output_format": "png",
	}, 2, 1)

	var imageRequest dto.ImageRequest
	require.NoError(t, common.UnmarshalBodyReusable(c, &imageRequest))
	require.NoError(t, validatePlaygroundImageRequest(imageRequest))

	names, hasMask, maskName, err := validatePlaygroundImageInput(c, playgroundImageModeEdit)
	require.NoError(t, err)
	require.Len(t, names, 2)
	require.Equal(t, "reference.png", names[0])
	require.True(t, hasMask)
	require.Equal(t, "mask.png", maskName)
}

func TestValidatePlaygroundImageEditRequiresImage(t *testing.T) {
	c := newPlaygroundImageMultipartContext(t, map[string]string{
		"model":  "gpt-image-2",
		"prompt": "replace the background",
	}, 0, 0)

	_, _, _, err := validatePlaygroundImageInput(c, playgroundImageModeEdit)
	require.ErrorContains(t, err, "image is required")
}

func TestValidatePlaygroundImageRequiresPrompt(t *testing.T) {
	c := newPlaygroundImageMultipartContext(t, map[string]string{
		"model": "gpt-image-2",
	}, 1, 0)

	var imageRequest dto.ImageRequest
	require.NoError(t, common.UnmarshalBodyReusable(c, &imageRequest))
	require.ErrorContains(t, validatePlaygroundImageRequest(imageRequest), "model and prompt are required")
}

func TestValidatePlaygroundImageGenerationDoesNotRequireMultipartImage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	names, hasMask, maskName, err := validatePlaygroundImageInput(c, playgroundImageModeGenerate)
	require.NoError(t, err)
	require.Empty(t, names)
	require.False(t, hasMask)
	require.Empty(t, maskName)
}
