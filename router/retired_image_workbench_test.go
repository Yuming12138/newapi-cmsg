package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayRoutesRetireImageWorkbench(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		http.MethodPost + " /pg/images/generations",
		http.MethodPost + " /pg/images/edits",
		http.MethodGet + " /pg/images/tasks",
		http.MethodGet + " /pg/images/tasks/:id",
		http.MethodPost + " /pg/images/tasks/:id/cancel",
	} {
		require.False(t, routes[route], "retired route registered: %s", route)
	}
	for _, route := range []string{
		http.MethodPost + " /pg/chat/completions",
		http.MethodPost + " /v1/responses",
		http.MethodPost + " /v1/chat/completions",
		http.MethodPost + " /v1/images/generations",
		http.MethodPost + " /v1/images/edits",
	} {
		require.True(t, routes[route], "general gateway route removed: %s", route)
	}
}
