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

func TestDeepSeekV4OfficialPricingRatios(t *testing.T) {
	tests := []struct {
		model           string
		modelRatio      float64
		completionRatio float64
		cacheRatio      float64
	}{
		{model: "deepseek-v4-flash", modelRatio: 0.14 / 2, completionRatio: 0.28 / 0.14, cacheRatio: 0.0028 / 0.14},
		{model: "deepseek-v4-pro", modelRatio: 0.435 / 2, completionRatio: 0.87 / 0.435, cacheRatio: 0.003625 / 0.435},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := defaultModelRatio[tt.model]; got != tt.modelRatio {
				t.Fatalf("model ratio = %v, want %v", got, tt.modelRatio)
			}
			if got := defaultCompletionRatio[tt.model]; got != tt.completionRatio {
				t.Fatalf("completion ratio = %v, want %v", got, tt.completionRatio)
			}
			if got := defaultCacheRatio[tt.model]; got != tt.cacheRatio {
				t.Fatalf("cache ratio = %v, want %v", got, tt.cacheRatio)
			}
		})
	}
}

func TestGPTImage15UsesImage2Ratios(t *testing.T) {
	if got := defaultModelRatio["gpt-image-1.5"]; got != defaultModelRatio["gpt-image-2"] {
		t.Fatalf("model ratio = %v, want %v", got, defaultModelRatio["gpt-image-2"])
	}
	if got := defaultCompletionRatio["gpt-image-1.5"]; got != defaultCompletionRatio["gpt-image-2"] {
		t.Fatalf("completion ratio = %v, want %v", got, defaultCompletionRatio["gpt-image-2"])
	}
	if got := defaultCacheRatio["gpt-image-1.5"]; got != defaultCacheRatio["gpt-image-2"] {
		t.Fatalf("cache ratio = %v, want %v", got, defaultCacheRatio["gpt-image-2"])
	}
}
