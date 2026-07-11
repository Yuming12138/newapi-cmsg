package model

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRedeemFixture(t *testing.T, quota int) (userID int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))

	user := &User{
		Username: fmt.Sprintf("redeem-%d", quota),
		Password: "password",
		Status:   common.UserStatusEnabled,
		AffCode:  fmt.Sprintf("redeem-aff-%d", quota),
		Quota:    0,
	}
	require.NoError(t, DB.Create(user).Error)

	key = fmt.Sprintf("%032d", quota)
	redemption := &Redemption{
		Name:        fmt.Sprintf("redeem-test-%d", quota),
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	t.Cleanup(func() {
		DB.Where("user_id = ?", user.Id).Delete(&Log{})
		DB.Unscoped().Delete(&Redemption{}, redemption.Id)
		DB.Unscoped().Delete(&User{}, user.Id)
	})
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userID, key := setupRedeemFixture(t, 500)

	quota, err := Redeem(key, userID)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userID).Error)
	assert.Equal(t, 500, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&redemption).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userID, redemption.UsedUserId)

	_, err = Redeem(key, userID)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userID).Error)
	assert.Equal(t, 500, user.Quota)
}

// SQLite skips SELECT ... FOR UPDATE, so the status compare-and-swap must be
// sufficient to ensure that only one concurrent redemption credits the user.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userID, key := setupRedeemFixture(t, 300)

	const goroutines = 5
	var successes atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(goroutines)
	for range goroutines {
		go func() {
			defer waitGroup.Done()
			if _, err := Redeem(key, userID); err == nil {
				successes.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	assert.EqualValues(t, 1, successes.Load(), "exactly one concurrent redeem should succeed")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userID).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}

func TestRedeemStatusCompareAndSwapRejectsSecondTransition(t *testing.T) {
	userID, key := setupRedeemFixture(t, 200)

	var redemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&redemption).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Update("status", common.RedemptionCodeStatusUsed)
		require.NoError(t, result.Error)
		require.EqualValues(t, 1, result.RowsAffected)

		second := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Update("used_user_id", userID)
		require.NoError(t, second.Error)
		assert.EqualValues(t, 0, second.RowsAffected)
		return nil
	}))
}
