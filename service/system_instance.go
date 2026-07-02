package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const systemInstanceReportInterval = 30 * time.Second

var systemInstanceReporterOnce sync.Once

type SystemInstanceInfo struct {
	SchemaVersion int                       `json:"schema_version"`
	Node          SystemInstanceNodeInfo    `json:"node"`
	Role          SystemInstanceRoleInfo    `json:"role"`
	Runtime       SystemInstanceRuntimeInfo `json:"runtime"`
	Host          SystemInstanceHostInfo    `json:"host"`
	Resources     SystemInstanceResources   `json:"resources,omitempty"`
}

type SystemInstanceNodeInfo struct {
	Name                    string `json:"name"`
	Source                  string `json:"source"`
	ManuallyConfigured      bool   `json:"manually_configured"`
	ShouldConfigureManually bool   `json:"should_configure_manually"`
}

type SystemInstanceRoleInfo struct {
	IsMaster bool `json:"is_master"`
}

type SystemInstanceRuntimeInfo struct {
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	StartedAt int64  `json:"started_at"`
}

type SystemInstanceHostInfo struct {
	Hostname string `json:"hostname"`
}

type SystemInstanceResources struct {
	CPU     SystemInstanceResourceUsage  `json:"cpu"`
	Memory  SystemInstanceResourceUsage  `json:"memory"`
	Storage SystemInstanceStorageMetrics `json:"storage"`
}

type SystemInstanceResourceUsage struct {
	UsagePercent float64 `json:"usage_percent"`
}

type SystemInstanceStorageMetrics struct {
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

func StartSystemInstanceReporter() {
	systemInstanceReporterOnce.Do(func() {
		gopool.Go(func() {
			reportSystemInstanceWithLog()

			ticker := time.NewTicker(systemInstanceReportInterval)
			defer ticker.Stop()
			for range ticker.C {
				reportSystemInstanceWithLog()
			}
		})
	})
}

func ReportCurrentSystemInstance() error {
	hostname, hostnameErr := os.Hostname()
	nodeName := strings.TrimSpace(common.NodeName)
	configuredNodeName := strings.TrimSpace(os.Getenv("NODE_NAME"))
	source := "NODE_NAME"
	manuallyConfigured := true
	shouldConfigureManually := false
	if configuredNodeName == "" {
		source = "hostname"
		manuallyConfigured = false
		shouldConfigureManually = true
	}
	if nodeName == "" {
		if hostnameErr != nil || strings.TrimSpace(hostname) == "" {
			return fmt.Errorf("system instance node name is empty")
		}
		nodeName = hostname
	}
	systemStatus := common.GetSystemStatus()
	diskInfo := common.GetDiskSpaceInfo()
	info := SystemInstanceInfo{
		SchemaVersion: 1,
		Node: SystemInstanceNodeInfo{
			Name:                    nodeName,
			Source:                  source,
			ManuallyConfigured:      manuallyConfigured,
			ShouldConfigureManually: shouldConfigureManually,
		},
		Role: SystemInstanceRoleInfo{
			IsMaster: common.IsMasterNode,
		},
		Runtime: SystemInstanceRuntimeInfo{
			Version:   common.Version,
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			StartedAt: common.StartTime,
		},
		Host: SystemInstanceHostInfo{
			Hostname: hostname,
		},
		Resources: SystemInstanceResources{
			CPU: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.CPUUsage,
			},
			Memory: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.MemoryUsage,
			},
			Storage: SystemInstanceStorageMetrics{
				TotalBytes:  diskInfo.Total,
				UsedBytes:   diskInfo.Used,
				FreeBytes:   diskInfo.Free,
				UsedPercent: diskInfo.UsedPercent,
			},
		},
	}
	return model.UpsertSystemInstance(nodeName, info, common.StartTime, common.GetTimestamp())
}

func reportSystemInstanceWithLog() {
	if err := ReportCurrentSystemInstance(); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("system instance report failed: %v", err))
	}
}
