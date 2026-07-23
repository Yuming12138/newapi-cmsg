package helper

import (
	"errors"
	"fmt"
	"strings"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func ResolveMappedModelName(modelMapping, originModelName string, isResponsesCompact bool) (string, bool, error) {
	mappingModelName := originModelName
	if isResponsesCompact && strings.HasSuffix(originModelName, ratio_setting.CompactModelSuffix) {
		mappingModelName = strings.TrimSuffix(originModelName, ratio_setting.CompactModelSuffix)
	}
	if modelMapping == "" || modelMapping == "{}" {
		return mappingModelName, false, nil
	}

	modelMap := make(map[string]string)
	if err := appcommon.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
		return "", false, fmt.Errorf("unmarshal_model_mapping_failed")
	}

	currentModel := mappingModelName
	visitedModels := map[string]bool{currentModel: true}
	isModelMapped := false
	for {
		mappedModel, exists := modelMap[currentModel]
		if !exists || mappedModel == "" {
			break
		}
		if visitedModels[mappedModel] {
			if mappedModel == currentModel {
				return currentModel, isModelMapped, nil
			}
			return "", false, errors.New("model_mapping_contains_cycle")
		}
		visitedModels[mappedModel] = true
		currentModel = mappedModel
		isModelMapped = true
	}
	return currentModel, isModelMapped, nil
}

func ApplyMappedBillingModel(info *relaycommon.RelayInfo, modelMapping string) error {
	if info == nil || info.ChannelMeta == nil || !info.ChannelOtherSettings.BillingByMappedModelEnabled {
		return nil
	}
	mappedModel, isMapped, err := ResolveMappedModelName(
		modelMapping,
		info.OriginModelName,
		info.RelayMode == relayconstant.RelayModeResponsesCompact,
	)
	if err != nil {
		return err
	}
	if isMapped {
		info.BillingModelName = mappedModel
	}
	return nil
}

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	isResponsesCompact := info.RelayMode == relayconstant.RelayModeResponsesCompact
	originModelName := info.OriginModelName
	mappingModelName := originModelName
	if isResponsesCompact && strings.HasSuffix(originModelName, ratio_setting.CompactModelSuffix) {
		mappingModelName = strings.TrimSuffix(originModelName, ratio_setting.CompactModelSuffix)
	}

	modelMapping := c.GetString("model_mapping")
	mappedModelName, isModelMapped, err := ResolveMappedModelName(modelMapping, originModelName, isResponsesCompact)
	if err != nil {
		return err
	}
	info.IsModelMapped = isModelMapped
	if isModelMapped {
		info.UpstreamModelName = mappedModelName
		if info.ChannelOtherSettings.BillingByMappedModelEnabled {
			info.BillingModelName = mappedModelName
		}
	}

	if isResponsesCompact {
		finalUpstreamModelName := mappingModelName
		if info.IsModelMapped && info.UpstreamModelName != "" {
			finalUpstreamModelName = info.UpstreamModelName
		}
		info.UpstreamModelName = finalUpstreamModelName
		if !info.ChannelOtherSettings.BillingByMappedModelEnabled {
			info.OriginModelName = ratio_setting.WithCompactModelSuffix(finalUpstreamModelName)
		}
	}
	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
