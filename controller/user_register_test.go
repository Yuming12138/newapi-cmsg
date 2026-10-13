package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
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

	var user model.User
	require.NoError(t, model.DB.Where("username = ?", "reg-open").First(&user).Error)
	require.Equal(t, "cmsg", user.Group)
	require.Zero(t, user.Quota)
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
	var user model.User
	require.NoError(t, model.DB.Where("username = ?", "reg-valid").First(&user).Error)
	require.Equal(t, "cmsg", user.Group)
}

func TestRegisterCannotOverrideDefaultCMSGGroup(t *testing.T) {
	for _, requestedGroup := range []string{"", "default", "vip", "asxs", "cmsg"} {
		t.Run(requestedGroup, func(t *testing.T) {
			setupUserRegisterControllerTest(t)
			body, err := common.Marshal(map[string]any{
				"username": "reg-group", "password": "password123", "group": requestedGroup,
			})
			require.NoError(t, err)
			ctx, recorder := newUserRegisterContext(string(body))
			Register(ctx)
			resp := decodeUserRegisterAPIResponse(t, recorder)
			require.True(t, resp.Success, resp.Message)
			var user model.User
			require.NoError(t, model.DB.Where("username = ?", "reg-group").First(&user).Error)
			require.Equal(t, "cmsg", user.Group)
			require.Equal(t, common.RoleCommonUser, user.Role)
		})
	}
}

func TestAdminCreateUserDefaultsToCMSGAndPreservesExplicitGroup(t *testing.T) {
	for _, tt := range []struct {
		group string
		want  string
	}{
		{"", "cmsg"},
		{" \t ", "cmsg"},
		{"default", "default"},
		{"cmsg", "cmsg"},
	} {
		t.Run(tt.group, func(t *testing.T) {
			setupUserRegisterControllerTest(t)
			body, err := common.Marshal(map[string]any{
				"username": "admin-created", "password": "password123",
				"role": common.RoleCommonUser, "group": tt.group,
			})
			require.NoError(t, err)
			ctx, recorder := newUserRegisterContext(string(body))
			ctx.Set("role", common.RoleRootUser)
			CreateUser(ctx)
			resp := decodeUserRegisterAPIResponse(t, recorder)
			require.True(t, resp.Success, resp.Message)
			var user model.User
			require.NoError(t, model.DB.Where("username = ?", "admin-created").First(&user).Error)
			require.Equal(t, tt.want, user.Group)
		})
	}
}

func TestOAuthRegistrationDefaultsToCMSGAndPreservesExistingGroup(t *testing.T) {
	providers := []struct {
		name     string
		provider oauth.Provider
	}{
		{"github", &oauth.GitHubProvider{}},
		{"discord", &oauth.DiscordProvider{}},
		{"oidc", &oauth.OIDCProvider{}},
		{"linuxdo", &oauth.LinuxDOProvider{}},
		{"custom", oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
			Id: 1, Name: "Test OAuth", Slug: "custom", Enabled: true,
		})},
	}
	for _, tt := range providers {
		for _, username := range []string{"oauth-new-user", ""} {
			t.Run(tt.name+"/"+username, func(t *testing.T) {
				setupUserRegisterControllerTest(t)
				oauthUser := &oauth.OAuthUser{
					ProviderUserID: "provider-user-1",
					Username:       username,
				}
				var user *model.User
				router := gin.New()
				router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-session-secret"))))
				router.GET("/", func(c *gin.Context) {
					var err error
					user, err = findOrCreateOAuthUser(c, tt.provider, oauthUser, sessions.Default(c))
					require.NoError(t, err)
				})
				request := func() {
					router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
				}

				request()
				require.NotNil(t, user)
				var stored model.User
				require.NoError(t, model.DB.First(&stored, user.Id).Error)
				require.Equal(t, "cmsg", stored.Group)
				require.Equal(t, common.RoleCommonUser, stored.Role)
				require.Empty(t, stored.Password)

				// Logging in again must not reassign an existing user's group.
				require.NoError(t, model.DB.Model(&stored).Update("group", "default").Error)
				common.RegisterEnabled = false
				request()
				require.Equal(t, stored.Id, user.Id)
				require.Equal(t, "default", user.Group)
				var count int64
				require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}
