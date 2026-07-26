package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type userDeleteAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    int    `json:"data"`
}

func setupUserDeleteControllerTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldRedisEnabled := common.RedisEnabled
	oldTranslateMessage := common.TranslateMessage

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserOAuthBinding{},
		&model.Token{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.PasskeyCredential{},
	))

	model.DB = db
	common.RedisEnabled = false
	common.TranslateMessage = func(_ *gin.Context, key string, _ ...map[string]any) string {
		return key
	}

	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedisEnabled
		common.TranslateMessage = oldTranslateMessage
	})
}

func newUserDeleteContext(method, target, body string, role int) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("role", role)
	return ctx, recorder
}

func decodeUserDeleteAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) userDeleteAPIResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var resp userDeleteAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	return resp
}

func TestDeleteUserReturnsSuccessOnHardDelete(t *testing.T) {
	setupUserDeleteControllerTest(t)

	require.NoError(t, model.DB.Create(&model.User{
		Id:       2,
		Username: "delete-target",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		AffCode:  "delete-target",
	}).Error)

	ctx, recorder := newUserDeleteContext(http.MethodDelete, "/api/user/2", "", common.RoleRootUser)
	ctx.Params = gin.Params{{Key: "id", Value: "2"}}

	DeleteUser(ctx)

	resp := decodeUserDeleteAPIResponse(t, recorder)
	require.True(t, resp.Success)

	var count int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id = ?", 2).Count(&count).Error)
	require.Zero(t, count)
}

func TestDeleteUserBatchPreflightsPermissions(t *testing.T) {
	setupUserDeleteControllerTest(t)

	require.NoError(t, model.DB.Create(&[]model.User{
		{
			Id:       2,
			Username: "batch-common",
			Password: "password",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
			AffCode:  "batch-common",
		},
		{
			Id:       3,
			Username: "batch-admin",
			Password: "password",
			Role:     common.RoleAdminUser,
			Status:   common.UserStatusEnabled,
			AffCode:  "batch-admin",
		},
	}).Error)

	ctx, recorder := newUserDeleteContext(http.MethodPost, "/api/user/batch", `{"ids":[2,3]}`, common.RoleAdminUser)

	DeleteUserBatch(ctx)

	resp := decodeUserDeleteAPIResponse(t, recorder)
	require.False(t, resp.Success)
	require.Equal(t, "user.no_permission_higher_level", resp.Message)

	var count int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id IN ?", []int{2, 3}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
