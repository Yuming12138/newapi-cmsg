package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryStopsWhenRequestContextIsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	upstreamErr := types.NewError(
		errors.New("upstream response header timeout"),
		types.ErrorCodeChannelResponseTimeExceeded,
		types.ErrOptionWithStatusCode(http.StatusGatewayTimeout),
	)

	require.False(t, shouldRetry(c, upstreamErr, 1))
}
