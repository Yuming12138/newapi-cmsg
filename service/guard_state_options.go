package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func loadJSONOption(key string, target interface{}) bool {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return false
	}
	if err := common.UnmarshalJsonStr(raw, target); err != nil {
		common.SysError("failed to unmarshal option " + key + ": " + err.Error())
		return false
	}
	return true
}

func saveJSONOption(key string, value interface{}) error {
	raw, err := common.Marshal(value)
	if err != nil {
		return err
	}
	return model.UpdateOption(key, string(raw))
}
