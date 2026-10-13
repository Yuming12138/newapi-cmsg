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

func TestDeleteUserPurgesSoftDeletedUserAndAuthenticationData(t *testing.T) {
	setupUserDeleteControllerTest(t)

	user := model.User{
		Id:       42,
		Username: "soft-delete-target",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		AffCode:  "soft-delete-target",
	}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "test-soft-delete-token"}
	require.NoError(t, model.DB.Create(&token).Error)
	require.NoError(t, model.DB.Delete(&token).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "test-secret"}).Error)
	require.NoError(t, model.DB.Create(&model.TwoFABackupCode{UserId: user.Id, CodeHash: "test-hash"}).Error)
	require.NoError(t, model.DB.Create(&model.PasskeyCredential{
		UserID: user.Id, CredentialID: "test-credential", PublicKey: "test-public-key",
	}).Error)
	require.NoError(t, model.DB.Create(&model.UserOAuthBinding{
		UserId: user.Id, ProviderId: 1, ProviderUserId: "test-provider-user",
	}).Error)
	require.NoError(t, model.DB.Delete(&user).Error)
	_, err := model.GetUserById(user.Id, false)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	ctx, recorder := newUserDeleteContext(http.MethodDelete, "/api/user/42", "", common.RoleRootUser)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	DeleteUser(ctx)

	resp := decodeUserDeleteAPIResponse(t, recorder)
	require.True(t, resp.Success, resp.Message)
	var count int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id = ?", user.Id).Count(&count).Error)
	require.Zero(t, count)
	for _, record := range []any{
		&model.Token{}, &model.TwoFA{}, &model.TwoFABackupCode{},
		&model.PasskeyCredential{}, &model.UserOAuthBinding{},
	} {
		require.NoError(t, model.DB.Unscoped().Model(record).Where("user_id = ?", user.Id).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestDeleteUserRetainsRoleProtectionForSoftDeletedUsers(t *testing.T) {
	for _, tt := range []struct {
		name       string
		callerRole int
		targetRole int
	}{
		{"admin cannot delete admin", common.RoleAdminUser, common.RoleAdminUser},
		{"admin cannot delete root", common.RoleAdminUser, common.RoleRootUser},
		{"root cannot delete root", common.RoleRootUser, common.RoleRootUser},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setupUserDeleteControllerTest(t)
			user := model.User{Id: 2, Username: "protected", Password: "password", Role: tt.targetRole}
			require.NoError(t, model.DB.Create(&user).Error)
			require.NoError(t, model.DB.Delete(&user).Error)

			ctx, recorder := newUserDeleteContext(http.MethodDelete, "/api/user/2", "", tt.callerRole)
			ctx.Params = gin.Params{{Key: "id", Value: "2"}}
			DeleteUser(ctx)

			resp := decodeUserDeleteAPIResponse(t, recorder)
			require.False(t, resp.Success)
			require.Equal(t, "user.no_permission_higher_level", resp.Message)
			var count int64
			require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id = ?", user.Id).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestDeleteUserRejectsMissingOrInvalidID(t *testing.T) {
	for _, id := range []string{"999999", "0", "-1", "invalid"} {
		t.Run(id, func(t *testing.T) {
			setupUserDeleteControllerTest(t)
			ctx, recorder := newUserDeleteContext(http.MethodDelete, "/api/user/"+id, "", common.RoleRootUser)
			ctx.Params = gin.Params{{Key: "id", Value: id}}
			DeleteUser(ctx)
			resp := decodeUserDeleteAPIResponse(t, recorder)
			require.False(t, resp.Success)
			if id == "999999" {
				require.Equal(t, gorm.ErrRecordNotFound.Error(), resp.Message)
			}
		})
	}
}

func TestDeleteUserBatchAcceptsMixedSoftDeletedAndActiveUsers(t *testing.T) {
	setupUserDeleteControllerTest(t)
	users := []model.User{
		{Id: 2, Username: "batch-active", Password: "password", Role: common.RoleCommonUser, AffCode: "batch-active"},
		{Id: 3, Username: "batch-deleted", Password: "password", Role: common.RoleCommonUser, AffCode: "batch-deleted"},
	}
	require.NoError(t, model.DB.Create(&users).Error)
	require.NoError(t, model.DB.Delete(&users[1]).Error)

	ctx, recorder := newUserDeleteContext(http.MethodPost, "/api/user/batch", `{"ids":[2,3,3]}`, common.RoleAdminUser)
	DeleteUserBatch(ctx)
	resp := decodeUserDeleteAPIResponse(t, recorder)
	require.True(t, resp.Success, resp.Message)
	require.Equal(t, 2, resp.Data)
	var count int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id IN ?", []int{2, 3}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDeleteUserBatchPreflightsSoftDeletedUserPermissions(t *testing.T) {
	setupUserDeleteControllerTest(t)
	users := []model.User{
		{Id: 2, Username: "batch-common", Password: "password", Role: common.RoleCommonUser, AffCode: "batch-common"},
		{Id: 3, Username: "batch-protected", Password: "password", Role: common.RoleAdminUser, AffCode: "batch-protected"},
	}
	require.NoError(t, model.DB.Create(&users).Error)
	require.NoError(t, model.DB.Delete(&users[1]).Error)
	ctx, recorder := newUserDeleteContext(http.MethodPost, "/api/user/batch", `{"ids":[2,3]}`, common.RoleAdminUser)
	DeleteUserBatch(ctx)
	resp := decodeUserDeleteAPIResponse(t, recorder)
	require.False(t, resp.Success)
	require.Equal(t, "user.no_permission_higher_level", resp.Message)
	var count int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id IN ?", []int{2, 3}).Count(&count).Error)
	require.EqualValues(t, 2, count)
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
