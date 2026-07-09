package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type userRegisterAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func setupUserRegisterControllerTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldRedisEnabled := common.RedisEnabled
	oldTranslateMessage := common.TranslateMessage
	oldRegisterEnabled := common.RegisterEnabled
	oldPasswordRegisterEnabled := common.PasswordRegisterEnabled
	oldRegistrationCodeEnabled := common.RegistrationCodeEnabled
	oldRegistrationCodes := common.RegistrationCodes
	oldEmailVerificationEnabled := common.EmailVerificationEnabled
	oldQuotaForNewUser := common.QuotaForNewUser
	oldGenerateDefaultToken := constant.GenerateDefaultToken

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserOAuthBinding{}, &model.Token{}, &model.Log{}))

	model.DB = db
	common.RedisEnabled = false
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.RegistrationCodeEnabled = false
	common.RegistrationCodes = ""
	common.EmailVerificationEnabled = false
	common.QuotaForNewUser = 0
	constant.GenerateDefaultToken = false
	common.TranslateMessage = func(_ *gin.Context, key string, _ ...map[string]any) string {
		return key
	}

	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedisEnabled
		common.TranslateMessage = oldTranslateMessage
		common.RegisterEnabled = oldRegisterEnabled
		common.PasswordRegisterEnabled = oldPasswordRegisterEnabled
		common.RegistrationCodeEnabled = oldRegistrationCodeEnabled
		common.RegistrationCodes = oldRegistrationCodes
		common.EmailVerificationEnabled = oldEmailVerificationEnabled
		common.QuotaForNewUser = oldQuotaForNewUser
		constant.GenerateDefaultToken = oldGenerateDefaultToken
	})
}

func newUserRegisterContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, recorder
}

func decodeUserRegisterAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) userRegisterAPIResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var resp userRegisterAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	return resp
}

func TestRegisterAllowsMissingRegistrationCodeWhenGateDisabled(t *testing.T) {
	setupUserRegisterControllerTest(t)

	ctx, recorder := newUserRegisterContext(`{"username":"reg-open","password":"password123"}`)
	Register(ctx)

	resp := decodeUserRegisterAPIResponse(t, recorder)
	require.True(t, resp.Success)

	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", "reg-open").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestRegisterRejectsMissingRegistrationCodeWhenGateEnabled(t *testing.T) {
	setupUserRegisterControllerTest(t)
	common.RegistrationCodeEnabled = true
	common.RegistrationCodes = "group-code"

	ctx, recorder := newUserRegisterContext(`{"username":"reg-missing","password":"password123"}`)
	Register(ctx)

	resp := decodeUserRegisterAPIResponse(t, recorder)
	require.False(t, resp.Success)
	require.Equal(t, "user.registration_code_required", resp.Message)
}

func TestRegisterRejectsWhenRegistrationCodeGateHasNoCodes(t *testing.T) {
	setupUserRegisterControllerTest(t)
	common.RegistrationCodeEnabled = true
	common.RegistrationCodes = " \n ; , "

	ctx, recorder := newUserRegisterContext(`{"username":"reg-no-codes","password":"password123","registration_code":"any"}`)
	Register(ctx)

	resp := decodeUserRegisterAPIResponse(t, recorder)
	require.False(t, resp.Success)
	require.Equal(t, "user.registration_code_not_configured", resp.Message)
}

func TestRegisterRejectsInvalidRegistrationCode(t *testing.T) {
	setupUserRegisterControllerTest(t)
	common.RegistrationCodeEnabled = true
	common.RegistrationCodes = "group-code"

	ctx, recorder := newUserRegisterContext(`{"username":"reg-invalid","password":"password123","registration_code":"wrong"}`)
	Register(ctx)

	resp := decodeUserRegisterAPIResponse(t, recorder)
	require.False(t, resp.Success)
	require.Equal(t, "user.registration_code_invalid", resp.Message)
}

func TestRegisterAcceptsValidRegistrationCode(t *testing.T) {
	setupUserRegisterControllerTest(t)
	common.RegistrationCodeEnabled = true
	common.RegistrationCodes = "alpha\n group-code ; beta"

	ctx, recorder := newUserRegisterContext(`{"username":"reg-valid","password":"password123","registration_code":"group-code"}`)
	Register(ctx)

	resp := decodeUserRegisterAPIResponse(t, recorder)
	require.True(t, resp.Success)

	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", "reg-valid").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
