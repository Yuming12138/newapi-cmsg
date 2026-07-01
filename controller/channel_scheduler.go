package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type channelSchedulerRuntimeItem struct {
	ChannelID                 int                                  `json:"channel_id"`
	ChannelName               string                               `json:"channel_name"`
	ChannelType               int                                  `json:"channel_type"`
	ChannelStatus             int                                  `json:"channel_status"`
	Group                     string                               `json:"group"`
	Tag                       *string                              `json:"tag,omitempty"`
	Priority                  int64                                `json:"priority"`
	Weight                    int                                  `json:"weight"`
	ResponseTime              int                                  `json:"response_time"`
	InFlight                  int                                  `json:"in_flight"`
	LatencyEWMAMs             float64                              `json:"latency_ewma_ms"`
	ErrorEWMA                 float64                              `json:"error_ewma"`
	HasLatencyEWMA            bool                                 `json:"has_latency_ewma"`
	HasErrorEWMA              bool                                 `json:"has_error_ewma"`
	LastStatusCode            int                                  `json:"last_status_code"`
	LastFailureUnix           int64                                `json:"last_failure_unix"`
	LastSuccessUnix           int64                                `json:"last_success_unix"`
	SuccessCount              int64                                `json:"success_count"`
	FailureCount              int64                                `json:"failure_count"`
	AttemptCount              int64                                `json:"attempt_count"`
	FailureRate               float64                              `json:"failure_rate"`
	Score                     float64                              `json:"score"`
	TemporaryUnschedulable    *model.ChannelTemporaryUnschedulable `json:"temporary_unschedulable,omitempty"`
	TemporaryUnschedulableNow bool                                 `json:"temporary_unschedulable_now"`
}

func GetChannelSchedulerRuntimeStats(c *gin.Context) {
	includeIdle := c.DefaultQuery("include_idle", "false") == "true"
	onlyUnhealthy := c.DefaultQuery("only_unhealthy", "false") == "true"
	channelID, _ := strconv.Atoi(strings.TrimSpace(c.Query("channel_id")))

	query := model.DB.Model(&model.Channel{}).Omit("key")
	if channelID > 0 {
		query = query.Where("id = ?", channelID)
	}

	var channels []*model.Channel
	if err := query.Find(&channels).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	snapshots := model.GetChannelRuntimeStatsSnapshots(channels, includeIdle || channelID > 0)
	channelByID := make(map[int]*model.Channel, len(channels))
	for _, channel := range channels {
		if channel != nil {
			channelByID[channel.Id] = channel
		}
	}

	items := make([]channelSchedulerRuntimeItem, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if onlyUnhealthy && !snapshot.TemporaryUnschedulableNow && snapshot.FailureCount == 0 && snapshot.InFlight == 0 {
			continue
		}
		channel := channelByID[snapshot.ChannelID]
		if channel == nil {
			continue
		}
		items = append(items, channelSchedulerRuntimeItem{
			ChannelID:                 channel.Id,
			ChannelName:               channel.Name,
			ChannelType:               channel.Type,
			ChannelStatus:             channel.Status,
			Group:                     channel.Group,
			Tag:                       channel.Tag,
			Priority:                  channel.GetPriority(),
			Weight:                    channel.GetWeight(),
			ResponseTime:              channel.ResponseTime,
			InFlight:                  snapshot.InFlight,
			LatencyEWMAMs:             snapshot.LatencyEWMAMs,
			ErrorEWMA:                 snapshot.ErrorEWMA,
			HasLatencyEWMA:            snapshot.HasLatencyEWMA,
			HasErrorEWMA:              snapshot.HasErrorEWMA,
			LastStatusCode:            snapshot.LastStatusCode,
			LastFailureUnix:           snapshot.LastFailureUnix,
			LastSuccessUnix:           snapshot.LastSuccessUnix,
			SuccessCount:              snapshot.SuccessCount,
			FailureCount:              snapshot.FailureCount,
			AttemptCount:              snapshot.AttemptCount,
			FailureRate:               snapshot.FailureRate,
			Score:                     snapshot.Score,
			TemporaryUnschedulable:    snapshot.TemporaryUnschedulable,
			TemporaryUnschedulableNow: snapshot.TemporaryUnschedulableNow,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": items,
			"total": len(items),
		},
	})
}
