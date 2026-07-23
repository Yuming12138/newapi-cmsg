package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveMappedModelName(t *testing.T) {
	tests := []struct {
		name       string
		mapping    string
		origin     string
		compact    bool
		wantModel  string
		wantMapped bool
		wantErr    string
	}{
		{
			name:       "ordinary mapping",
			mapping:    `{"gpt-5.5":"grok-4.5"}`,
			origin:     "gpt-5.5",
			wantModel:  "grok-4.5",
			wantMapped: true,
		},
		{
			name:       "compact mapping strips distributor suffix",
			mapping:    `{"gpt-5.5":"grok-4.5"}`,
			origin:     "gpt-5.5-openai-compact",
			compact:    true,
			wantModel:  "grok-4.5",
			wantMapped: true,
		},
		{
			name:       "chained mapping",
			mapping:    `{"gpt-5.5":"alias","alias":"grok-4.5"}`,
			origin:     "gpt-5.5",
			wantModel:  "grok-4.5",
			wantMapped: true,
		},
		{
			name:      "self mapping is ignored",
			mapping:   `{"gpt-5.5":"gpt-5.5"}`,
			origin:    "gpt-5.5",
			wantModel: "gpt-5.5",
		},
		{
			name:    "cycle is rejected",
			mapping: `{"a":"b","b":"a"}`,
			origin:  "a",
			wantErr: "model_mapping_contains_cycle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModel, gotMapped, err := ResolveMappedModelName(tt.mapping, tt.origin, tt.compact)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantModel, gotModel)
			require.Equal(t, tt.wantMapped, gotMapped)
		})
	}
}

func TestApplyMappedBillingModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5-openai-compact",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				BillingByMappedModelEnabled: true,
			},
		},
	}

	require.NoError(t, ApplyMappedBillingModel(info, `{"gpt-5.5":"grok-4.5"}`))
	require.Equal(t, "grok-4.5", info.BillingModelName)
	require.Equal(t, "gpt-5.5-openai-compact", info.OriginModelName)
}

func TestApplyMappedBillingModelUsesBaseModelForCompactWithoutMapping(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5-openai-compact",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				BillingByMappedModelEnabled: true,
			},
		},
	}

	require.NoError(t, ApplyMappedBillingModel(info, ""))
	require.Equal(t, "gpt-5.5", info.BillingModelName)
	require.Equal(t, "gpt-5.5-openai-compact", info.OriginModelName)
}

func TestModelMappedHelperPreservesRequestedCompactModelForMappedBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("model_mapping", `{"gpt-5.5":"grok-4.5"}`)

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.5-openai-compact",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5-openai-compact",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				BillingByMappedModelEnabled: true,
			},
		},
	}

	require.NoError(t, ModelMappedHelper(ctx, info, nil))
	require.Equal(t, "gpt-5.5-openai-compact", info.OriginModelName)
	require.Equal(t, "grok-4.5", info.UpstreamModelName)
	require.Equal(t, "grok-4.5", info.BillingModelName)
}
