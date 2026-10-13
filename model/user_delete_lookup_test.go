package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetUserForAdminDeletionIncludesSoftDeletedUser(t *testing.T) {
	truncateTables(t)

	user := User{
		Username: "deletion-lookup",
		Password: "password",
		Email:    "deletion@example.test",
		Role:     common.RoleAdminUser,
	}
	user.SetAccessToken("test-deletion-access-token")
	require.NoError(t, DB.Create(&user).Error)

	for _, deleted := range []bool{false, true} {
		if deleted {
			require.NoError(t, DB.Delete(&user).Error)
		}
		loaded, err := GetUserForAdminDeletion(user.Id)
		require.NoError(t, err)
		require.Equal(t, user.Id, loaded.Id)
		require.Equal(t, common.RoleAdminUser, loaded.Role)
		require.Empty(t, loaded.Password)
		require.Empty(t, loaded.Email)
		require.Nil(t, loaded.AccessToken)
	}

	// Including deleted users is restricted to the deletion-specific lookup.
	for _, selectAll := range []bool{false, true} {
		_, err := GetUserById(user.Id, selectAll)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
}

func TestGetUserForAdminDeletionRejectsInvalidOrMissingID(t *testing.T) {
	truncateTables(t)

	for _, id := range []int{0, -1} {
		user, err := GetUserForAdminDeletion(id)
		require.Error(t, err)
		require.Nil(t, user)
	}
	user, err := GetUserForAdminDeletion(999999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Nil(t, user)
}
