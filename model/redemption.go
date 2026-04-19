package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"gorm.io/gorm"
)

type Redemption struct {
	Id                    int            `json:"id"`
	UserId                int            `json:"user_id"`
	Key                   string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status                int            `json:"status" gorm:"default:1"`
	Name                  string         `json:"name" gorm:"index"`
	Quota                 int            `json:"quota" gorm:"default:100"`
	SubscriptionPlanId    int            `json:"subscription_plan_id" gorm:"type:int;default:0;index"`
	SubscriptionPlanTitle string         `json:"subscription_plan_title" gorm:"-:all"`
	CreatedTime           int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime          int64          `json:"redeemed_time" gorm:"bigint"`
	Count                 int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId            int            `json:"used_user_id"`
	DeletedAt             gorm.DeletedAt `gorm:"index"`
	ExpiredTime           int64          `json:"expired_time" gorm:"bigint"`
}

type RedemptionResult struct {
	Quota                 int    `json:"quota"`
	SubscriptionPlanId    int    `json:"subscription_plan_id,omitempty"`
	SubscriptionPlanTitle string `json:"subscription_plan_title,omitempty"`
}

func hydrateRedemptionSubscriptionPlan(redemption *Redemption) {
	if redemption == nil || redemption.SubscriptionPlanId <= 0 {
		return
	}
	plan, err := GetSubscriptionPlanById(redemption.SubscriptionPlanId)
	if err != nil || plan == nil {
		return
	}
	redemption.SubscriptionPlanTitle = plan.Title
}

func hydrateRedemptionsSubscriptionPlan(redemptions []*Redemption) {
	for _, redemption := range redemptions {
		hydrateRedemptionSubscriptionPlan(redemption)
	}
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Model(&Redemption{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	hydrateRedemptionsSubscriptionPlan(redemptions)
	return redemptions, total, nil
}

func SearchRedemptions(keyword string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})
	if id, parseErr := strconv.Atoi(keyword); parseErr == nil {
		query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
	} else {
		query = query.Where("name LIKE ?", keyword+"%")
	}

	if err = query.Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	hydrateRedemptionsSubscriptionPlan(redemptions)
	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id is empty")
	}
	redemption := Redemption{Id: id}
	if err := DB.First(&redemption, "id = ?", id).Error; err != nil {
		return nil, err
	}
	hydrateRedemptionSubscriptionPlan(&redemption)
	return &redemption, nil
}

func Redeem(key string, userId int) (*RedemptionResult, error) {
	if key == "" {
		return nil, errors.New("redemption code is empty")
	}
	if userId == 0 {
		return nil, errors.New("invalid user id")
	}

	redemption := &Redemption{}
	result := &RedemptionResult{}
	subscriptionPlanTitle := ""

	keyCol := "`key`"
	if common.UsingPostgreSQL {
		keyCol = `"key"`
	}

	common.RandomSleep()
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where(keyCol+" = ?", key).
			First(redemption).Error; err != nil {
			return errors.New("invalid redemption code")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("redemption code is unavailable")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("redemption code has expired")
		}

		if redemption.Quota > 0 {
			if err := tx.Model(&User{}).
				Where("id = ?", userId).
				Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error; err != nil {
				return err
			}
		}

		if redemption.SubscriptionPlanId > 0 {
			plan, err := getSubscriptionPlanByIdTx(tx, redemption.SubscriptionPlanId)
			if err != nil {
				return err
			}
			subscriptionPlanTitle = plan.Title
			if _, err = CreateUserSubscriptionFromPlanTx(tx, userId, plan, "redemption"); err != nil {
				return err
			}
			result.SubscriptionPlanId = plan.Id
			result.SubscriptionPlanTitle = plan.Title
		}

		redemption.RedeemedTime = common.GetTimestamp()
		redemption.Status = common.RedemptionCodeStatusUsed
		redemption.UsedUserId = userId
		return tx.Save(redemption).Error
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return nil, ErrRedeemFailed
	}

	result.Quota = redemption.Quota
	if result.SubscriptionPlanTitle == "" {
		result.SubscriptionPlanId = redemption.SubscriptionPlanId
		result.SubscriptionPlanTitle = subscriptionPlanTitle
	}

	logParts := make([]string, 0, 2)
	if redemption.Quota > 0 {
		logParts = append(logParts, fmt.Sprintf("quota %s", logger.LogQuota(redemption.Quota)))
	}
	if subscriptionPlanTitle != "" {
		logParts = append(logParts, fmt.Sprintf("subscription %s", subscriptionPlanTitle))
	}
	if len(logParts) == 0 {
		logParts = append(logParts, "redeemed")
	}
	RecordLog(
		userId,
		LogTypeTopup,
		fmt.Sprintf("Redeemed code with %s, redemption ID %d", strings.Join(logParts, ", "), redemption.Id),
	)

	return result, nil
}

func (redemption *Redemption) Insert() error {
	return DB.Create(redemption).Error
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values.
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update requires the editable fields to be filled before calling.
func (redemption *Redemption) Update() error {
	return DB.Model(redemption).
		Select("name", "status", "quota", "subscription_plan_id", "redeemed_time", "expired_time").
		Updates(redemption).Error
}

func (redemption *Redemption) Delete() error {
	return DB.Delete(redemption).Error
}

func DeleteRedemptionById(id int) error {
	if id == 0 {
		return errors.New("id is empty")
	}
	redemption := Redemption{Id: id}
	if err := DB.Where(redemption).First(&redemption).Error; err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where(
		"status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)",
		[]int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled},
		common.RedemptionCodeStatusEnabled,
		now,
	).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
