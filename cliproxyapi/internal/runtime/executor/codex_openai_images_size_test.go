package executor

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/tidwall/gjson"
)

func encodeImageForSizeTest(t *testing.T, format string, width int, height int) string {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	default:
		t.Fatalf("unsupported test format %q", format)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func encodeVP8XHeaderForSizeTest(width int, height int) string {
	header := make([]byte, 30)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(header)-8))
	copy(header[8:12], "WEBP")
	copy(header[12:16], "VP8X")
	header[24] = byte(width - 1)
	header[25] = byte((width - 1) >> 8)
	header[26] = byte((width - 1) >> 16)
	header[27] = byte(height - 1)
	header[28] = byte((height - 1) >> 8)
	header[29] = byte((height - 1) >> 16)
	return base64.StdEncoding.EncodeToString(header)
}

func TestCodexImageDimensionsFromBase64(t *testing.T) {
	tests := []struct {
		name string
		b64  string
		want string
	}{
		{name: "png", b64: encodeImageForSizeTest(t, "png", 1536, 1024), want: "1536x1024"},
		{name: "jpeg data URL", b64: "data:image/jpeg;base64," + encodeImageForSizeTest(t, "jpeg", 1024, 1536), want: "1024x1536"},
		{name: "webp VP8X", b64: encodeVP8XHeaderForSizeTest(1536, 1024), want: "1536x1024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := codexImageDimensionsFromBase64(tt.b64)
			if !ok || got != tt.want {
				t.Fatalf("dimensions = %q, %v; want %q, true", got, ok, tt.want)
			}
		})
	}
}

func TestCodexPrepareOpenAIImageRequestRejectsUnsupportedSizeBeforeUpstream(t *testing.T) {
	_, err := codexPrepareOpenAIImageGenerationJSON(
		[]byte(`{"model":"gpt-image-2","prompt":"test","size":"2048x1152"}`),
		"gpt-image-2",
	)
	if err == nil {
		t.Fatal("expected unsupported size error")
	}
	status, ok := err.(statusErr)
	if !ok || status.code != 400 {
		t.Fatalf("error = %#v, want statusErr 400", err)
	}
	if got := status.Error(); !bytes.Contains([]byte(got), []byte("supported sizes")) {
		t.Fatalf("error %q does not list supported sizes", got)
	}
}

func TestCodexPrepareOpenAIImageRequestAcceptsDeclaredSizes(t *testing.T) {
	for _, size := range []string{"", "auto", "1024x1024", "1536x1024", "1024x1536"} {
		payload := []byte(`{"model":"gpt-image-2","prompt":"test","size":"` + size + `"}`)
		if _, err := codexPrepareOpenAIImageGenerationJSON(payload, "gpt-image-2"); err != nil {
			t.Fatalf("size %q rejected: %v", size, err)
		}
	}
}

func TestCodexBuildImagesAPIResponseReportsActualSizeMismatch(t *testing.T) {
	imageB64 := encodeImageForSizeTest(t, "png", 1536, 1024)
	results := []codexImageCallResult{{Result: imageB64, OutputFormat: "png", ActualSize: "1536x1024"}}
	out, err := codexBuildImagesAPIResponse(results, 1710000000, nil, results[0], "b64_json", "2048x1152")
	if err != nil {
		t.Fatalf("codexBuildImagesAPIResponse: %v", err)
	}
	if got := gjson.GetBytes(out, "requested_size").String(); got != "2048x1152" {
		t.Fatalf("requested_size = %q", got)
	}
	if got := gjson.GetBytes(out, "actual_size").String(); got != "1536x1024" {
		t.Fatalf("actual_size = %q", got)
	}
	if !gjson.GetBytes(out, "size_mismatch").Bool() {
		t.Fatalf("size_mismatch missing: %s", string(out))
	}
	if got := gjson.GetBytes(out, "data.0.actual_size").String(); got != "1536x1024" {
		t.Fatalf("data.0.actual_size = %q", got)
	}
}

func TestCodexBuildImageCompletedFrameReportsActualSizeMismatch(t *testing.T) {
	frame := codexBuildImageCompletedFrame(
		codexImageCallResult{Result: "AA==", ActualSize: "1536x1024"},
		nil,
		"b64_json",
		"image_generation",
		"2048x1152",
	)
	payload := bytes.TrimSpace(bytes.SplitN(frame, []byte("data: "), 2)[1])
	if got := gjson.GetBytes(payload, "actual_size").String(); got != "1536x1024" {
		t.Fatalf("actual_size = %q; frame=%s", got, string(frame))
	}
	if !gjson.GetBytes(payload, "size_mismatch").Bool() {
		t.Fatalf("size_mismatch missing: %s", string(frame))
	}
}
