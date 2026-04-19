package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func truncateRedemptionTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		DB.Exec("DELETE FROM redemptions")
		DB.Exec("DELETE FROM subscription_plans")
		DB.Exec("DELETE FROM user_subscriptions")
		DB.Exec("DELETE FROM logs")
		DB.Exec("DELETE FROM users")
	})
}

func TestRedeemCreatesSubscriptionAndQuota(t *testing.T) {
	truncateRedemptionTables(t)
	initCol()

	require.NoError(t, DB.AutoMigrate(
		&User{},
		&Redemption{},
		&SubscriptionPlan{},
		&UserSubscription{},
		&Log{},
	))

	user := &User{
		Id:       1001,
		Username: "redeem_user",
		Password: "12345678",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1000,
	}
	require.NoError(t, DB.Create(user).Error)

	plan := &SubscriptionPlan{
		Id:            2001,
		Title:         "Starter Plan",
		Enabled:       true,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   5000,
	}
	require.NoError(t, DB.Create(plan).Error)

	redemption := &Redemption{
		Id:                 3001,
		UserId:             1,
		Key:                "redeem-code-001",
		Status:             common.RedemptionCodeStatusEnabled,
		Name:               "Starter Bundle",
		Quota:              2500,
		SubscriptionPlanId: plan.Id,
		CreatedTime:        common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := Redeem(redemption.Key, user.Id)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2500, result.Quota)
	assert.Equal(t, plan.Id, result.SubscriptionPlanId)
	assert.Equal(t, plan.Title, result.SubscriptionPlanTitle)

	var updatedUser User
	require.NoError(t, DB.Where("id = ?", user.Id).First(&updatedUser).Error)
	assert.Equal(t, 3500, updatedUser.Quota)

	var updatedRedemption Redemption
	require.NoError(t, DB.Where("id = ?", redemption.Id).First(&updatedRedemption).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, updatedRedemption.Status)
	assert.Equal(t, user.Id, updatedRedemption.UsedUserId)
	assert.NotZero(t, updatedRedemption.RedeemedTime)

	var subs []UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).Find(&subs).Error)
	require.Len(t, subs, 1)
	assert.Equal(t, "redemption", subs[0].Source)
	assert.Equal(t, int64(5000), subs[0].AmountTotal)

	fetched, err := GetRedemptionById(redemption.Id)
	require.NoError(t, err)
	assert.Equal(t, plan.Title, fetched.SubscriptionPlanTitle)
}
