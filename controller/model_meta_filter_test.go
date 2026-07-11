package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type modelMetaFilterResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items        []model.Model    `json:"items"`
		Total        int64            `json:"total"`
		Page         int              `json:"page"`
		PageSize     int              `json:"page_size"`
		VendorCounts map[string]int64 `json:"vendor_counts"`
	} `json:"data"`
}

type modelMetaHandlerFixture struct {
	alphaVendorID int
	betaVendorID  int
}

func seedModelMetaHandlerFixture(t *testing.T, db *gorm.DB) modelMetaHandlerFixture {
	t.Helper()

	alphaVendor := model.Vendor{Name: "Alpha Provider", Status: 1}
	betaVendor := model.Vendor{Name: "Beta Provider", Status: 1}
	require.NoError(t, db.Create(&alphaVendor).Error)
	require.NoError(t, db.Create(&betaVendor).Error)

	models := []*model.Model{
		{ModelName: "alpha-enabled-official", VendorID: alphaVendor.Id, Status: 1, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "alpha-enabled-manual", VendorID: alphaVendor.Id, Status: 1, SyncOfficial: 0, Endpoints: "[]"},
		{ModelName: "alpha-disabled-official", VendorID: alphaVendor.Id, Status: 0, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "beta-enabled-official", VendorID: betaVendor.Id, Status: 1, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "beta-disabled-manual", VendorID: betaVendor.Id, Status: 0, SyncOfficial: 0, Endpoints: "[]"},
	}
	for _, item := range models {
		require.NoError(t, item.Insert())
	}

	return modelMetaHandlerFixture{
		alphaVendorID: alphaVendor.Id,
		betaVendorID:  betaVendor.Id,
	}
}

func callModelMetaFilterHandler(t *testing.T, handler gin.HandlerFunc, query url.Values) modelMetaFilterResponse {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	handler(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response modelMetaFilterResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func TestGetAllModelsMetaAppliesFiltersWithoutChangingVendorCounts(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	fixture := seedModelMetaHandlerFixture(t, db)

	response := callModelMetaFilterHandler(t, GetAllModelsMeta, url.Values{
		"status":        {"enabled"},
		"sync_official": {"yes"},
		"p":             {"2"},
		"page_size":     {"1"},
	})

	require.Equal(t, int64(2), response.Data.Total)
	require.Equal(t, 2, response.Data.Page)
	require.Equal(t, 1, response.Data.PageSize)
	require.Len(t, response.Data.Items, 1)
	require.Equal(t, "alpha-enabled-official", response.Data.Items[0].ModelName)
	require.Equal(t, int64(3), response.Data.VendorCounts[fmtInt(fixture.alphaVendorID)])
	require.Equal(t, int64(2), response.Data.VendorCounts[fmtInt(fixture.betaVendorID)])
}

func TestSearchModelsMetaCombinesFiltersAndReturnsVendorCounts(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	fixture := seedModelMetaHandlerFixture(t, db)

	response := callModelMetaFilterHandler(t, SearchModelsMeta, url.Values{
		"keyword":       {"disabled"},
		"vendor":        {"Alpha Provider"},
		"status":        {"disabled"},
		"sync_official": {"yes"},
		"p":             {"1"},
		"page_size":     {"10"},
	})

	require.Equal(t, int64(1), response.Data.Total)
	require.Len(t, response.Data.Items, 1)
	require.Equal(t, "alpha-disabled-official", response.Data.Items[0].ModelName)
	require.Equal(t, int64(3), response.Data.VendorCounts[fmtInt(fixture.alphaVendorID)])
	require.Equal(t, int64(2), response.Data.VendorCounts[fmtInt(fixture.betaVendorID)])
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}
