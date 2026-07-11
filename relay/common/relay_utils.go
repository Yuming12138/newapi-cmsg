package common

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type HasPrompt interface {
	GetPrompt() string
}

type HasImage interface {
	HasImage() bool
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case constant.ChannelTypeOpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case constant.ChannelTypeAzure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	return fullRequestURL
}

// SanitizeURLForLog masks credentials carried in URL query parameters while
// preserving routing/debug information such as the scheme, host, path and
// non-sensitive query values.
func SanitizeURLForLog(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return sanitizeMalformedURLForLog(rawURL)
	}
	userinfoRemoved := parsedURL.User != nil
	parsedURL.User = nil

	if parsedURL.RawQuery == "" {
		if userinfoRemoved {
			return parsedURL.String()
		}
		return rawURL
	}

	query, err := url.ParseQuery(parsedURL.RawQuery)
	if err != nil {
		parsedURL.RawQuery = maskedURLQuery()
		return parsedURL.String()
	}

	queryChanged := false
	for key := range query {
		if isSensitiveURLQueryKey(key) {
			query.Set(key, "***masked***")
			queryChanged = true
		}
	}
	if !queryChanged && !userinfoRemoved {
		return rawURL
	}

	if queryChanged {
		parsedURL.RawQuery = query.Encode()
	}
	return parsedURL.String()
}

func sanitizeMalformedURLForLog(rawURL string) string {
	queryIndex := strings.IndexByte(rawURL, '?')
	if queryIndex < 0 {
		return stripRawURLUserinfo(rawURL)
	}

	prefix := stripRawURLUserinfo(rawURL[:queryIndex])
	fragment := ""
	if fragmentIndex := strings.IndexByte(rawURL[queryIndex+1:], '#'); fragmentIndex >= 0 {
		fragment = rawURL[queryIndex+1+fragmentIndex:]
	}
	return prefix + "?" + maskedURLQuery() + fragment
}

func stripRawURLUserinfo(rawURL string) string {
	authorityStart := 0
	if schemeIndex := strings.Index(rawURL, "://"); schemeIndex >= 0 {
		authorityStart = schemeIndex + 3
	}
	authorityEnd := len(rawURL)
	if separatorIndex := strings.IndexAny(rawURL[authorityStart:], "/?#"); separatorIndex >= 0 {
		authorityEnd = authorityStart + separatorIndex
	}
	authority := rawURL[authorityStart:authorityEnd]
	if atIndex := strings.LastIndexByte(authority, '@'); atIndex >= 0 {
		return rawURL[:authorityStart] + authority[atIndex+1:] + rawURL[authorityEnd:]
	}
	return rawURL
}

func maskedURLQuery() string {
	return url.Values{"_redacted_": {"***masked***"}}.Encode()
}

type sanitizedURLErrorLog struct {
	message string
	err     error
}

func (e *sanitizedURLErrorLog) Error() string {
	return e.message
}

func (e *sanitizedURLErrorLog) Unwrap() error {
	return e.err
}

func (e *sanitizedURLErrorLog) Timeout() bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(e.err, &timeoutErr) && timeoutErr.Timeout()
}

func (e *sanitizedURLErrorLog) Temporary() bool {
	var temporaryErr interface{ Temporary() bool }
	return errors.As(e.err, &temporaryErr) && temporaryErr.Temporary()
}

// SanitizeURLErrorForLog returns an error whose Error text contains sanitized
// URLs while its Unwrap chain retains the complete original error tree. This
// keeps errors.Is/errors.As behavior for wrapped and joined errors unchanged;
// errors.As still returns the original url.Error value.
func SanitizeURLErrorForLog(err error) error {
	if err == nil {
		return nil
	}

	urls := collectErrorURLs(err)
	if len(urls) == 0 {
		return err
	}

	safeMessage := err.Error()
	changed := false
	for _, rawURL := range urls {
		sanitizedURL := SanitizeURLForLog(rawURL)
		if sanitizedURL == rawURL {
			continue
		}
		changed = true
		safeMessage = strings.ReplaceAll(safeMessage, strconv.Quote(rawURL), strconv.Quote(sanitizedURL))
		safeMessage = strings.ReplaceAll(safeMessage, rawURL, sanitizedURL)
	}
	if !changed {
		return err
	}
	return &sanitizedURLErrorLog{message: safeMessage, err: err}
}

func collectErrorURLs(err error) []string {
	seen := make(map[string]struct{})
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		if urlErr, ok := current.(*url.Error); ok && urlErr.URL != "" {
			seen[urlErr.URL] = struct{}{}
		}
		switch unwrapped := current.(type) {
		case interface{ Unwrap() []error }:
			for _, nested := range unwrapped.Unwrap() {
				walk(nested)
			}
		case interface{ Unwrap() error }:
			walk(unwrapped.Unwrap())
		}
	}
	walk(err)

	urls := make([]string, 0, len(seen))
	for rawURL := range seen {
		urls = append(urls, rawURL)
	}
	sort.Slice(urls, func(i, j int) bool {
		return len(urls[i]) > len(urls[j])
	})
	return urls
}

func isSensitiveURLQueryKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "key",
		"api_key",
		"api-key",
		"apikey",
		"x-api-key",
		"access_token",
		"refresh_token",
		"id_token",
		"token",
		"authorization",
		"auth",
		"client_secret",
		"secret",
		"password",
		"passwd",
		"signature",
		"sig",
		"awsaccesskeyid",
		"x-amz-credential",
		"x-amz-security-token",
		"x-amz-signature":
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "signature")
}

func GetAPIVersion(c *gin.Context) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion == "" {
		apiVersion = c.GetString("api_version")
	}
	return apiVersion
}

func createTaskError(err error, code string, statusCode int, localError bool) *dto.TaskError {
	return &dto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: statusCode,
		LocalError: localError,
		Error:      err,
	}
}

func storeTaskRequest(c *gin.Context, info *RelayInfo, action string, requestObj TaskSubmitReq) {
	info.Action = action
	c.Set("task_request", requestObj)
}
func GetTaskRequest(c *gin.Context) (TaskSubmitReq, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return TaskSubmitReq{}, fmt.Errorf("request not found in context")
	}
	req, ok := v.(TaskSubmitReq)
	if !ok {
		return TaskSubmitReq{}, fmt.Errorf("invalid task request type")
	}
	return req, nil
}

func validatePrompt(prompt string) *dto.TaskError {
	if strings.TrimSpace(prompt) == "" {
		return createTaskError(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest, true)
	}
	return nil
}

func validateMultipartTaskRequest(c *gin.Context, info *RelayInfo, action string) (TaskSubmitReq, error) {
	var req TaskSubmitReq
	if _, err := c.MultipartForm(); err != nil {
		return req, err
	}

	formData := c.Request.PostForm
	req = TaskSubmitReq{
		Prompt:   formData.Get("prompt"),
		Model:    formData.Get("model"),
		Mode:     formData.Get("mode"),
		Image:    formData.Get("image"),
		Size:     formData.Get("size"),
		Metadata: make(map[string]interface{}),
	}

	if durationStr := formData.Get("seconds"); durationStr != "" {
		if duration, err := strconv.Atoi(durationStr); err == nil {
			req.Duration = duration
		}
	}

	if images := formData["images"]; len(images) > 0 {
		req.Images = images
	}

	for key, values := range formData {
		if len(values) > 0 && !isKnownTaskField(key) {
			if intVal, err := strconv.Atoi(values[0]); err == nil {
				req.Metadata[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(values[0], 64); err == nil {
				req.Metadata[key] = floatVal
			} else {
				req.Metadata[key] = values[0]
			}
		}
	}
	return req, nil
}

func ValidateMultipartDirect(c *gin.Context, info *RelayInfo) *dto.TaskError {
	var prompt string
	var model string
	var seconds int
	var size string
	var hasInputReference bool

	var req TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}

	prompt = req.Prompt
	model = req.Model
	size = req.Size
	seconds, _ = strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if req.InputReference != "" {
		req.Images = []string{req.InputReference}
	} else if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{strings.TrimSpace(req.Image)}
	}

	if strings.TrimSpace(req.Model) == "" {
		return createTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}

	if req.HasImage() {
		hasInputReference = true
	}

	if taskErr := validatePrompt(prompt); taskErr != nil {
		return taskErr
	}

	action := constant.TaskActionTextGenerate
	if hasInputReference {
		action = constant.TaskActionGenerate
	}
	if strings.HasPrefix(model, "sora-2") {

		if size == "" {
			size = "720x1280"
		}

		if seconds <= 0 {
			seconds = 4
		}

		if model == "sora-2" && !lo.Contains([]string{"720x1280", "1280x720"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		if model == "sora-2-pro" && !lo.Contains([]string{"720x1280", "1280x720", "1792x1024", "1024x1792"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		// OtherRatios 已移到 Sora adaptor 的 EstimateBilling 中设置
	}

	storeTaskRequest(c, info, action, req)

	return nil
}

func isKnownTaskField(field string) bool {
	knownFields := map[string]bool{
		"prompt":          true,
		"model":           true,
		"mode":            true,
		"image":           true,
		"images":          true,
		"size":            true,
		"duration":        true,
		"input_reference": true, // Sora 特有字段
	}
	return knownFields[field]
}

func ValidateBasicTaskRequest(c *gin.Context, info *RelayInfo, action string) *dto.TaskError {
	var err error
	contentType := c.GetHeader("Content-Type")
	var req TaskSubmitReq
	if strings.HasPrefix(contentType, "multipart/form-data") {
		req, err = validateMultipartTaskRequest(c, info, action)
		if err != nil {
			return createTaskError(err, "invalid_multipart_form", http.StatusBadRequest, true)
		}
	}
	// 为了metadata字段的兼容性，统一UnmarshalBodyReusable
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_request", http.StatusBadRequest, true)
	}

	if taskErr := validatePrompt(req.Prompt); taskErr != nil {
		return taskErr
	}

	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{req.Image}
	}

	storeTaskRequest(c, info, action, req)
	return nil
}
