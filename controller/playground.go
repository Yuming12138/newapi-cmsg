package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type playgroundImageTaskRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	N            uint   `json:"n,omitempty"`
}

type playgroundImageTaskData struct {
	Request  playgroundImageTaskRequest `json:"request"`
	Response json.RawMessage            `json:"response,omitempty"`
}

type playgroundImageTaskResponse struct {
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	Progress     string          `json:"progress"`
	FailReason   string          `json:"fail_reason,omitempty"`
	Model        string          `json:"model"`
	Group        string          `json:"group"`
	Prompt       string          `json:"prompt"`
	Size         string          `json:"size,omitempty"`
	Quality      string          `json:"quality,omitempty"`
	OutputFormat string          `json:"output_format,omitempty"`
	N            uint            `json:"n,omitempty"`
	SubmitTime   int64           `json:"submit_time"`
	StartTime    int64           `json:"start_time,omitempty"`
	FinishTime   int64           `json:"finish_time,omitempty"`
	Response     json.RawMessage `json:"response,omitempty"`
}

var playgroundImageTaskCancels sync.Map

func registerPlaygroundImageTaskCancel(taskID string, cancel context.CancelFunc) {
	if taskID == "" || cancel == nil {
		return
	}
	playgroundImageTaskCancels.Store(taskID, cancel)
}

func unregisterPlaygroundImageTaskCancel(taskID string) {
	if taskID == "" {
		return
	}
	playgroundImageTaskCancels.Delete(taskID)
}

func cancelPlaygroundImageTaskContext(taskID string) bool {
	value, ok := playgroundImageTaskCancels.Load(taskID)
	if !ok {
		return false
	}
	cancel, ok := value.(context.CancelFunc)
	if !ok {
		playgroundImageTaskCancels.Delete(taskID)
		return false
	}
	cancel()
	return true
}

func isPlaygroundImageTaskTerminal(status model.TaskStatus) bool {
	return status == model.TaskStatusSuccess ||
		status == model.TaskStatusFailure ||
		status == model.TaskStatusCancelled
}

func preparePlaygroundRelayContext(c *gin.Context, relayFormat types.RelayFormat) *types.NewAPIError {
	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		return types.NewError(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, nil, nil)
	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	if err = middleware.SetupContextForToken(c, tempToken); err != nil {
		return types.NewError(err, types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func playgroundRelay(c *gin.Context, relayFormat types.RelayFormat) {
	if newAPIError := preparePlaygroundRelayContext(c, relayFormat); newAPIError != nil {
		c.JSON(newAPIError.StatusCode, gin.H{
			"error": newAPIError.ToOpenAIError(),
		})
		return
	}
	Relay(c, relayFormat)
}

func Playground(c *gin.Context) {
	playgroundRelay(c, types.RelayFormatOpenAI)
}

func PlaygroundImage(c *gin.Context) {
	c.Set("skip_channel_affinity", true)

	if newAPIError := preparePlaygroundRelayContext(c, types.RelayFormatOpenAIImage); newAPIError != nil {
		c.JSON(newAPIError.StatusCode, gin.H{
			"error": newAPIError.ToOpenAIError(),
		})
		return
	}

	var imageRequest dto.ImageRequest
	if err := common.UnmarshalBodyReusable(c, &imageRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": err.Error()},
		})
		return
	}
	if strings.TrimSpace(imageRequest.Model) == "" || strings.TrimSpace(imageRequest.Prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "model and prompt are required"},
		})
		return
	}

	bodyStorage, err := common.GetBodyStorage(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": err.Error()},
		})
		return
	}
	requestBody, err := bodyStorage.Bytes()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": err.Error()},
		})
		return
	}

	n := uint(1)
	if imageRequest.N != nil && *imageRequest.N > 0 {
		n = *imageRequest.N
	}
	taskData := playgroundImageTaskData{
		Request: playgroundImageTaskRequest{
			Model:        imageRequest.Model,
			Prompt:       imageRequest.Prompt,
			Size:         imageRequest.Size,
			Quality:      imageRequest.Quality,
			OutputFormat: strings.Trim(string(imageRequest.OutputFormat), `"`),
			N:            n,
		},
	}

	now := time.Now().Unix()
	task := &model.Task{
		CreatedAt:  now,
		UpdatedAt:  now,
		TaskID:     model.GenerateTaskID(),
		Platform:   constant.TaskPlatformImage,
		UserId:     c.GetInt("id"),
		Group:      common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		ChannelId:  common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		Action:     "generate",
		Status:     model.TaskStatusQueued,
		SubmitTime: now,
		Progress:   "0%",
		Properties: model.Properties{
			Input:           imageRequest.Prompt,
			OriginModelName: imageRequest.Model,
		},
	}
	task.SetData(taskData)
	if err = task.Insert(); err != nil {
		logger.LogError(c, fmt.Sprintf("insert playground image task failed: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "failed to create image task"},
		})
		return
	}

	keys := clonePlaygroundTaskKeys(c)
	headers := c.Request.Header.Clone()
	remoteAddr := c.Request.RemoteAddr
	go runPlaygroundImageTask(task.TaskID, requestBody, keys, headers, remoteAddr)

	c.JSON(http.StatusAccepted, playgroundImageTaskToResponse(task))
}

func GetPlaygroundImageTask(c *gin.Context) {
	task, ok := loadPlaygroundImageTask(c, c.Param("id"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, playgroundImageTaskToResponse(task))
}

func CancelPlaygroundImageTask(c *gin.Context) {
	task, ok := loadPlaygroundImageTask(c, c.Param("id"))
	if !ok {
		return
	}
	if isPlaygroundImageTaskTerminal(task.Status) {
		c.JSON(http.StatusOK, playgroundImageTaskToResponse(task))
		return
	}

	fromStatus := task.Status
	now := time.Now().Unix()
	task.Status = model.TaskStatusCancelled
	task.Progress = "100%"
	task.FinishTime = now
	task.UpdatedAt = now
	task.FailReason = "cancelled by user"
	won, err := task.UpdateWithStatus(fromStatus)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("cancel playground image task failed: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "failed to cancel image task"},
		})
		return
	}
	if !won {
		latest, exists, err := model.GetByTaskId(c.GetInt("id"), task.TaskID)
		if err != nil {
			logger.LogError(c, fmt.Sprintf("reload playground image task after cancel race failed: %s", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{"message": "failed to cancel image task"},
			})
			return
		}
		if !exists || latest == nil {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"message": "image task not found"},
			})
			return
		}
		c.JSON(http.StatusOK, playgroundImageTaskToResponse(latest))
		return
	}

	cancelPlaygroundImageTaskContext(task.TaskID)
	c.JSON(http.StatusOK, playgroundImageTaskToResponse(task))
}

func ListPlaygroundImageTasks(c *gin.Context) {
	limit := common.String2Int(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	tasks := model.TaskGetAllUserTask(c.GetInt("id"), 0, limit, model.SyncTaskQueryParams{
		Platform: constant.TaskPlatformImage,
	})
	items := make([]playgroundImageTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, playgroundImageTaskToResponse(task))
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func loadPlaygroundImageTask(c *gin.Context, taskID string) (*model.Task, bool) {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("get playground image task failed: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "failed to get image task"},
		})
		return nil, false
	}
	if !exists || task == nil || task.Platform != constant.TaskPlatformImage {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"message": "image task not found"},
		})
		return nil, false
	}
	return task, true
}

func runPlaygroundImageTask(taskID string, requestBody []byte, keys map[string]any, headers http.Header, remoteAddr string) {
	requestCtx, cancel := context.WithCancel(context.Background())
	registerPlaygroundImageTaskCancel(taskID, cancel)
	defer func() {
		cancel()
		unregisterPlaygroundImageTaskCancel(taskID)
	}()

	task, exists, err := model.GetByOnlyTaskId(taskID)
	if err != nil || !exists || task == nil {
		return
	}
	if task.Status == model.TaskStatusCancelled {
		return
	}

	now := time.Now().Unix()
	task.Status = model.TaskStatusInProgress
	task.Progress = "5%"
	task.StartTime = now
	task.UpdatedAt = now
	won, err := task.UpdateWithStatus(model.TaskStatusQueued)
	if err != nil || !won {
		return
	}

	recorder := httptest.NewRecorder()
	taskCtx, _ := gin.CreateTestContext(recorder)
	taskCtx.Request = httptest.NewRequest(http.MethodPost, "/pg/images/generations", bytes.NewReader(requestBody)).WithContext(requestCtx)
	taskCtx.Request.Header = headers.Clone()
	if taskCtx.Request.Header.Get("Content-Type") == "" {
		taskCtx.Request.Header.Set("Content-Type", "application/json")
	}
	taskCtx.Request.RemoteAddr = remoteAddr
	taskCtx.Keys = keys
	common.SetContextKey(taskCtx, constant.ContextKeyRequestStartTime, time.Now())
	defer common.CleanupBodyStorage(taskCtx)

	Relay(taskCtx, types.RelayFormatOpenAIImage)

	statusCode := recorder.Code
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	responseBody := recorder.Body.Bytes()

	task, exists, err = model.GetByOnlyTaskId(taskID)
	if err != nil || !exists || task == nil {
		return
	}
	if task.Status != model.TaskStatusInProgress {
		return
	}
	task.UpdatedAt = time.Now().Unix()
	task.FinishTime = task.UpdatedAt
	task.Progress = "100%"

	if statusCode >= http.StatusOK && statusCode < http.StatusBadRequest {
		data := playgroundImageTaskData{}
		_ = task.GetData(&data)
		data.Response = append(json.RawMessage(nil), responseBody...)
		task.SetData(data)
		task.Status = model.TaskStatusSuccess
		task.FailReason = ""
		if won, err = task.UpdateWithStatus(model.TaskStatusInProgress); err != nil {
			logger.LogError(context.Background(), fmt.Sprintf("complete playground image task failed: %s", err.Error()))
		} else if !won {
			return
		}
		return
	}

	task.Status = model.TaskStatusFailure
	task.FailReason = summarizePlaygroundImageError(statusCode, responseBody)
	if won, err = task.UpdateWithStatus(model.TaskStatusInProgress); err != nil {
		logger.LogError(context.Background(), fmt.Sprintf("fail playground image task failed: %s", err.Error()))
	} else if !won {
		return
	}
}

func clonePlaygroundTaskKeys(c *gin.Context) map[string]any {
	keys := make(map[string]any, len(c.Keys))
	for key, value := range c.Keys {
		if key == common.KeyBodyStorage || key == common.KeyRequestBody {
			continue
		}
		keys[key] = value
	}
	return keys
}

func playgroundImageTaskToResponse(task *model.Task) playgroundImageTaskResponse {
	data := playgroundImageTaskData{}
	_ = task.GetData(&data)
	modelName := data.Request.Model
	if modelName == "" {
		modelName = task.Properties.OriginModelName
	}
	return playgroundImageTaskResponse{
		TaskID:       task.TaskID,
		Status:       string(task.Status),
		Progress:     task.Progress,
		FailReason:   task.FailReason,
		Model:        modelName,
		Group:        task.Group,
		Prompt:       common.GetStringIfEmpty(data.Request.Prompt, task.Properties.Input),
		Size:         data.Request.Size,
		Quality:      data.Request.Quality,
		OutputFormat: data.Request.OutputFormat,
		N:            data.Request.N,
		SubmitTime:   task.SubmitTime,
		StartTime:    task.StartTime,
		FinishTime:   task.FinishTime,
		Response:     data.Response,
	}
}

func summarizePlaygroundImageError(statusCode int, responseBody []byte) string {
	body := strings.TrimSpace(string(responseBody))
	var payload struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &payload); err == nil {
		if payload.Error.Message != "" {
			body = payload.Error.Message
		} else if payload.Message != "" {
			body = payload.Message
		}
	}
	if len(body) > 800 {
		body = body[:800]
	}
	if body == "" {
		body = http.StatusText(statusCode)
	}
	return fmt.Sprintf("status_code=%d, %s", statusCode, body)
}
