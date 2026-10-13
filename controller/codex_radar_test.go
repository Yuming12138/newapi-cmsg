package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func TestCodexRadarControllerUnavailableHasRecoveryDelay(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/codex-radar/overview", nil)
	getCodexRadarOverview(c, func(context.Context) (service.CodexRadarOverview, error) {
		return service.CodexRadarOverview{}, errors.New("public upstream timeout")
	})
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 502 || w.Header().Get("Retry-After") != "60" || response.Success || response.Message != "Codex Radar data is temporarily unavailable" {
		t.Fatalf("status=%d delay=%s response=%#v", w.Code, w.Header().Get("Retry-After"), response)
	}
}

func TestCodexRadarControllerReturnsUsableCachedData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/codex-radar/overview", nil)
	want := service.CodexRadarOverview{
		SchemaVersion: "radar-bench-v2-150", FetchedAt: "2026-10-14T00:00:00Z",
		Stale: true, Metrics: make([]service.CodexRadarMetric, 24),
	}
	getCodexRadarOverview(c, func(context.Context) (service.CodexRadarOverview, error) { return want, nil })
	var response struct {
		Success bool                       `json:"success"`
		Data    service.CodexRadarOverview `json:"data"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Header().Get("Retry-After") != "" || !response.Success || !response.Data.Stale || response.Data.FetchedAt != want.FetchedAt || len(response.Data.Metrics) != 24 {
		t.Fatalf("status=%d response=%#v", w.Code, response)
	}
}
