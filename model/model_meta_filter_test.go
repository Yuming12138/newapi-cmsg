package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type modelMetaFilterFixture struct {
	alphaVendorID int
	betaVendorID  int
}

func setupModelMetaFilterTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	oldDB := DB
	oldLogDB := LOG_DB
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	oldUsingPostgreSQL := common.UsingPostgreSQL

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(&Vendor{}, &Model{}))

	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		common.UsingPostgreSQL = oldUsingPostgreSQL
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func seedModelMetaFilterFixture(t *testing.T, db *gorm.DB) modelMetaFilterFixture {
	t.Helper()

	alphaVendor := Vendor{Name: "Alpha Provider", Status: 1}
	betaVendor := Vendor{Name: "Beta Provider", Status: 1}
	require.NoError(t, db.Create(&alphaVendor).Error)
	require.NoError(t, db.Create(&betaVendor).Error)

	models := []*Model{
		{ModelName: "alpha-enabled-official", VendorID: alphaVendor.Id, Status: 1, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "alpha-enabled-manual", VendorID: alphaVendor.Id, Status: 1, SyncOfficial: 0, Endpoints: "[]"},
		{ModelName: "alpha-disabled-official", VendorID: alphaVendor.Id, Status: 0, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "beta-enabled-official", VendorID: betaVendor.Id, Status: 1, SyncOfficial: 1, Endpoints: "[]"},
		{ModelName: "beta-disabled-manual", VendorID: betaVendor.Id, Status: 0, SyncOfficial: 0, Endpoints: "[]"},
	}
	for _, item := range models {
		require.NoError(t, item.Insert())
	}

	return modelMetaFilterFixture{
		alphaVendorID: alphaVendor.Id,
		betaVendorID:  betaVendor.Id,
	}
}

func modelNames(items []*Model) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.ModelName)
	}
	return names
}

func TestSearchModelsAppliesStatusAndSyncFilters(t *testing.T) {
	db := setupModelMetaFilterTestDB(t)
	seedModelMetaFilterFixture(t, db)

	testCases := []struct {
		name         string
		status       string
		syncOfficial string
		wantNames    []string
	}{
		{
			name:      "enabled textual status",
			status:    "enabled",
			wantNames: []string{"beta-enabled-official", "alpha-enabled-manual", "alpha-enabled-official"},
		},
		{
			name:         "official sync textual value",
			syncOfficial: "yes",
			wantNames:    []string{"beta-enabled-official", "alpha-disabled-official", "alpha-enabled-official"},
		},
		{
			name:         "disabled and manual combination",
			status:       "0",
			syncOfficial: "no",
			wantNames:    []string{"beta-disabled-manual"},
		},
		{
			name:         "enabled and official combination",
			status:       "1",
			syncOfficial: "1",
			wantNames:    []string{"beta-enabled-official", "alpha-enabled-official"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			items, total, err := SearchModels("", "", testCase.status, testCase.syncOfficial, 0, 100)
			require.NoError(t, err)
			require.Equal(t, int64(len(testCase.wantNames)), total)
			require.Equal(t, testCase.wantNames, modelNames(items))
		})
	}
}

func TestSearchModelsKeepsFilteredTotalAcrossPagination(t *testing.T) {
	db := setupModelMetaFilterTestDB(t)
	fixture := seedModelMetaFilterFixture(t, db)

	items, total, err := SearchModels("", "", "enabled", "", 1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, []string{"alpha-enabled-manual"}, modelNames(items))

	vendorCounts, err := GetVendorModelCounts()
	require.NoError(t, err)
	require.Equal(t, map[int64]int64{
		int64(fixture.alphaVendorID): 3,
		int64(fixture.betaVendorID):  2,
	}, vendorCounts)
}

func TestSearchModelsCombinesKeywordVendorStatusAndSyncFilters(t *testing.T) {
	db := setupModelMetaFilterTestDB(t)
	seedModelMetaFilterFixture(t, db)

	items, total, err := SearchModels("disabled", "Alpha Provider", "disabled", "yes", 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, []string{"alpha-disabled-official"}, modelNames(items))
}
