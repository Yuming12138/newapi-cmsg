package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const userQuotaRequestStateOptionKey = "user_quota_request_state"

const (
	UserQuotaRequestStatusPending  = "pending"
	UserQuotaRequestStatusApproved = "approved"
	UserQuotaRequestStatusRejected = "rejected"
)

const (
	userQuotaRequestAmountUSD      = 20.0
	userQuotaRequestThresholdUSD   = 10.0
	userQuotaRequestMaxPerDay      = 2
	userQuotaRequestAutoApproveMax = 1
)

type UserQuotaRequest struct {
	ID                string  `json:"id"`
	Date              string  `json:"date"`
	UserID            int     `json:"user_id"`
	Username          string  `json:"username,omitempty"`
	DisplayName       string  `json:"display_name,omitempty"`
	AmountUSD         float64 `json:"amount_usd"`
	Status            string  `json:"status"`
	AutoApproved      bool    `json:"auto_approved,omitempty"`
	Note              string  `json:"note,omitempty"`
	AdminID           int     `json:"admin_id,omitempty"`
	AdminUsername     string  `json:"admin_username,omitempty"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
	ReviewedAt        int64   `json:"reviewed_at,omitempty"`
	RequestCount      int     `json:"request_count,omitempty"`
	RemainingRequests int     `json:"remaining_requests,omitempty"`
}

type UserQuotaRequestStatus struct {
	Enabled            bool               `json:"enabled"`
	CanRequest         bool               `json:"can_request"`
	Reason             string             `json:"reason,omitempty"`
	Date               string             `json:"date"`
	QuotaPerUSD        int                `json:"quota_per_usd"`
	ThresholdUSD       float64            `json:"threshold_usd"`
	ThresholdQuota     int                `json:"threshold_quota"`
	RequestAmountUSD   float64            `json:"request_amount_usd"`
	RequestAmountQuota int                `json:"request_amount_quota"`
	MaxRequestsPerDay  int                `json:"max_requests_per_day"`
	AutoApproveLimit   int                `json:"auto_approve_limit"`
	RequestCount       int                `json:"request_count"`
	RemainingRequests  int                `json:"remaining_requests"`
	LatestRequest      *UserQuotaRequest  `json:"latest_request,omitempty"`
	Requests           []UserQuotaRequest `json:"requests,omitempty"`
}

type UserQuotaRequestReview struct {
	RequestID     string `json:"request_id"`
	Approve       bool   `json:"approve"`
	Note          string `json:"note,omitempty"`
	AdminID       int    `json:"admin_id"`
	AdminUsername string `json:"admin_username,omitempty"`
}

type userQuotaRequestState struct {
	Version int                                      `json:"version"`
	Days    map[string]map[string][]UserQuotaRequest `json:"days"`
}

var userQuotaRequestMu sync.Mutex

func GetSelfUserQuotaRequestStatus(userID int, currentQuota int) (UserQuotaRequestStatus, error) {
	userQuotaRequestMu.Lock()
	defer userQuotaRequestMu.Unlock()

	cfg := operation_setting.GetUserQuotaGuardSetting()
	dateKey := userQuotaRequestDateKey(cfg)
	state := loadUserQuotaRequestState()
	requests := append([]UserQuotaRequest(nil), state.Days[dateKey][strconv.Itoa(userID)]...)
	quotaPerUSD := userQuotaGuardQuotaPerUSD(cfg)
	return buildUserQuotaRequestStatus(cfg, dateKey, quotaPerUSD, currentQuota, requests), nil
}

func CreateSelfUserQuotaRequest(user *model.User) (UserQuotaRequestStatus, error) {
	if user == nil || user.Id <= 0 {
		return UserQuotaRequestStatus{}, fmt.Errorf("invalid user")
	}

	userQuotaRequestMu.Lock()
	defer userQuotaRequestMu.Unlock()

	cfg := operation_setting.GetUserQuotaGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return UserQuotaRequestStatus{}, fmt.Errorf("quota request is not available")
	}

	dateKey := guardDateString(cfg.Timezone)
	quotaPerUSD := userQuotaGuardQuotaPerUSD(cfg)
	state := loadUserQuotaRequestState()
	ensureUserQuotaRequestDay(&state, dateKey)

	userKey := strconv.Itoa(user.Id)
	requests := append([]UserQuotaRequest(nil), state.Days[dateKey][userKey]...)
	status := buildUserQuotaRequestStatus(cfg, dateKey, quotaPerUSD, user.Quota, requests)
	if !status.CanRequest {
		return status, fmt.Errorf("%s", status.Reason)
	}

	now := common.GetTimestamp()
	request := UserQuotaRequest{
		ID:                fmt.Sprintf("%s-%d-%d", dateKey, user.Id, len(requests)+1),
		Date:              dateKey,
		UserID:            user.Id,
		Username:          user.Username,
		DisplayName:       user.DisplayName,
		AmountUSD:         userQuotaRequestAmountUSD,
		Status:            UserQuotaRequestStatusPending,
		CreatedAt:         now,
		UpdatedAt:         now,
		RequestCount:      len(requests) + 1,
		RemainingRequests: userQuotaRequestMaxPerDay - len(requests) - 1,
	}

	if len(requests) < userQuotaRequestAutoApproveMax {
		request.Status = UserQuotaRequestStatusApproved
		request.AutoApproved = true
		request.Note = "first low-balance request auto-approved"
		request.ReviewedAt = now
		if err := addUserQuotaGuardDailyApproval(cfg, dateKey, user.Id, request.AmountUSD, request.Note, now); err != nil {
			return status, err
		}
	}

	requests = append(requests, request)
	state.Days[dateKey][userKey] = requests
	if err := saveJSONOption(userQuotaRequestStateOptionKey, state); err != nil {
		return status, err
	}
	if request.Status == UserQuotaRequestStatusApproved {
		runUserQuotaGuardOnce()
	}

	status = buildUserQuotaRequestStatus(cfg, dateKey, quotaPerUSD, user.Quota, requests)
	return status, nil
}

func ListPendingUserQuotaRequests() ([]UserQuotaRequest, error) {
	userQuotaRequestMu.Lock()
	defer userQuotaRequestMu.Unlock()

	state := loadUserQuotaRequestState()
	result := make([]UserQuotaRequest, 0)
	for _, day := range state.Days {
		for _, requests := range day {
			for _, request := range requests {
				if request.Status == UserQuotaRequestStatusPending {
					result = append(result, request)
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt == result[j].CreatedAt {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

func ReviewUserQuotaRequest(review UserQuotaRequestReview) (UserQuotaRequest, error) {
	if strings.TrimSpace(review.RequestID) == "" {
		return UserQuotaRequest{}, fmt.Errorf("request_id is required")
	}

	userQuotaRequestMu.Lock()
	defer userQuotaRequestMu.Unlock()

	cfg := operation_setting.GetUserQuotaGuardSetting()
	state := loadUserQuotaRequestState()
	now := common.GetTimestamp()

	for dateKey, day := range state.Days {
		for userKey, requests := range day {
			for idx := range requests {
				if requests[idx].ID != review.RequestID {
					continue
				}
				if requests[idx].Status != UserQuotaRequestStatusPending {
					return requests[idx], fmt.Errorf("request is already %s", requests[idx].Status)
				}
				requests[idx].UpdatedAt = now
				requests[idx].ReviewedAt = now
				requests[idx].AdminID = review.AdminID
				requests[idx].AdminUsername = review.AdminUsername
				requests[idx].Note = strings.TrimSpace(review.Note)
				if review.Approve {
					requests[idx].Status = UserQuotaRequestStatusApproved
					if requests[idx].Note == "" {
						requests[idx].Note = "approved by admin"
					}
					if err := addUserQuotaGuardDailyApproval(cfg, dateKey, requests[idx].UserID, requests[idx].AmountUSD, requests[idx].Note, now); err != nil {
						return requests[idx], err
					}
				} else {
					requests[idx].Status = UserQuotaRequestStatusRejected
					if requests[idx].Note == "" {
						requests[idx].Note = "rejected by admin"
					}
				}
				day[userKey] = requests
				if err := saveJSONOption(userQuotaRequestStateOptionKey, state); err != nil {
					return requests[idx], err
				}
				if review.Approve {
					runUserQuotaGuardOnce()
				}
				return requests[idx], nil
			}
		}
	}

	return UserQuotaRequest{}, fmt.Errorf("request not found")
}

func buildUserQuotaRequestStatus(cfg *operation_setting.UserQuotaGuardSetting, dateKey string, quotaPerUSD int, currentQuota int, requests []UserQuotaRequest) UserQuotaRequestStatus {
	thresholdQuota := quotaFromUSD(userQuotaRequestThresholdUSD, quotaPerUSD)
	requestAmountQuota := quotaFromUSD(userQuotaRequestAmountUSD, quotaPerUSD)
	requestCount := len(requests)
	remaining := userQuotaRequestMaxPerDay - requestCount
	if remaining < 0 {
		remaining = 0
	}

	status := UserQuotaRequestStatus{
		Enabled:            cfg != nil && cfg.Enabled,
		Date:               dateKey,
		QuotaPerUSD:        quotaPerUSD,
		ThresholdUSD:       userQuotaRequestThresholdUSD,
		ThresholdQuota:     thresholdQuota,
		RequestAmountUSD:   userQuotaRequestAmountUSD,
		RequestAmountQuota: requestAmountQuota,
		MaxRequestsPerDay:  userQuotaRequestMaxPerDay,
		AutoApproveLimit:   userQuotaRequestAutoApproveMax,
		RequestCount:       requestCount,
		RemainingRequests:  remaining,
		Requests:           requests,
	}
	if requestCount > 0 {
		latest := requests[requestCount-1]
		status.LatestRequest = &latest
	}
	switch {
	case cfg == nil || !cfg.Enabled:
		status.Reason = "quota guard is not enabled"
	case currentQuota >= thresholdQuota:
		status.Reason = fmt.Sprintf("current quota is not below $%g", userQuotaRequestThresholdUSD)
	case requestCount >= userQuotaRequestMaxPerDay:
		status.Reason = "daily request limit reached"
	default:
		status.CanRequest = true
	}
	return status
}

func loadUserQuotaRequestState() userQuotaRequestState {
	state := userQuotaRequestState{Version: 1, Days: map[string]map[string][]UserQuotaRequest{}}
	if loadJSONOption(userQuotaRequestStateOptionKey, &state) && state.Days != nil {
		state.Version = 1
		return state
	}
	return state
}

func ensureUserQuotaRequestDay(state *userQuotaRequestState, dateKey string) {
	if state.Days == nil {
		state.Days = map[string]map[string][]UserQuotaRequest{}
	}
	if state.Days[dateKey] == nil {
		state.Days[dateKey] = map[string][]UserQuotaRequest{}
	}
}

func userQuotaRequestDateKey(cfg *operation_setting.UserQuotaGuardSetting) string {
	if cfg == nil {
		return guardDateString("")
	}
	return guardDateString(cfg.Timezone)
}

func addUserQuotaGuardDailyApproval(cfg *operation_setting.UserQuotaGuardSetting, dateKey string, userID int, extraUSD float64, note string, updatedAt int64) error {
	if cfg == nil {
		return fmt.Errorf("quota guard setting is not available")
	}
	if cfg.DailyApprovals == nil {
		cfg.DailyApprovals = map[string]map[string]operation_setting.UserQuotaGuardApproval{}
	}
	if cfg.DailyApprovals[dateKey] == nil {
		cfg.DailyApprovals[dateKey] = map[string]operation_setting.UserQuotaGuardApproval{}
	}
	userKey := strconv.Itoa(userID)
	existing := cfg.DailyApprovals[dateKey][userKey]
	existing.ExtraUSD += extraUSD
	existing.UpdatedAt = updatedAt
	if strings.TrimSpace(note) != "" {
		if strings.TrimSpace(existing.Note) != "" {
			existing.Note = existing.Note + "; " + strings.TrimSpace(note)
		} else {
			existing.Note = strings.TrimSpace(note)
		}
	}
	cfg.DailyApprovals[dateKey][userKey] = existing

	raw, err := common.Marshal(cfg.DailyApprovals)
	if err != nil {
		return err
	}
	if err := model.UpdateOption("user_quota_guard_setting.daily_approvals", string(raw)); err != nil {
		return err
	}
	return nil
}
