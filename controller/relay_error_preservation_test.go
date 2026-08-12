package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestPreferLastRelayErrorPreservesRealUpstreamFailure(t *testing.T) {
	upstream := types.WithOpenAIError(types.OpenAIError{
		Message: "auth_unavailable: no auth available",
		Type:    "upstream_error",
		Code:    "auth_unavailable",
	}, http.StatusBadGateway)
	selection := types.NewErrorWithStatusCode(
		errors.New("no channel found"),
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)

	got := preferLastRelayError(selection, upstream)
	require.Same(t, upstream, got)
	require.Equal(t, http.StatusBadGateway, got.StatusCode)
	require.Equal(t, types.ErrorCode("auth_unavailable"), got.GetErrorCode())
}

func TestPreferLastRelayErrorUsesInitialSelectionFailure(t *testing.T) {
	selection := types.NewErrorWithStatusCode(
		errors.New("no channel found"),
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)

	got := preferLastRelayError(selection, nil)
	require.Same(t, selection, got)
	require.Equal(t, http.StatusServiceUnavailable, got.StatusCode)
}
