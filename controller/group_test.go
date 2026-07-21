package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userIdentityGroupsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

func TestGetUserIdentityGroupsReturnsOnlyAssignableUserGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/group/user_identity", nil)

	GetUserIdentityGroups(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response userIdentityGroupsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, []string{"default", "cmsg"}, response.Data)
	require.NotContains(t, response.Data, "asxs")
	require.NotContains(t, response.Data, "cliproxy-codex")
}
