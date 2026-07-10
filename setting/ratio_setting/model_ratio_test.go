package ratio_setting

import "testing"

func TestGPT56OfficialPricingRatios(t *testing.T) {
	tests := []struct {
		model      string
		modelRatio float64
	}{
		{model: "gpt-5.6-sol", modelRatio: 2.5},
		{model: "gpt-5.6-terra", modelRatio: 1.25},
		{model: "gpt-5.6-luna", modelRatio: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			modelRatio, ok := defaultModelRatio[tt.model]
			if modelRatio != tt.modelRatio {
				t.Fatalf("model ratio = %v, want %v", modelRatio, tt.modelRatio)
			}

			if completionRatio := GetCompletionRatio(tt.model); completionRatio != 6 {
				t.Fatalf("completion ratio = %v, want 6", completionRatio)
			}

			cacheRatio, ok := defaultCacheRatio[tt.model]
			if !ok {
				t.Fatalf("expected cache ratio for %s", tt.model)
			}
			if cacheRatio != 0.1 {
				t.Fatalf("cache ratio = %v, want 0.1", cacheRatio)
			}

			createCacheRatio, ok := defaultCreateCacheRatio[tt.model]
			if !ok {
				t.Fatalf("expected create cache ratio for %s", tt.model)
			}
			if createCacheRatio != 1.25 {
				t.Fatalf("create cache ratio = %v, want 1.25", createCacheRatio)
			}
		})
	}
}
