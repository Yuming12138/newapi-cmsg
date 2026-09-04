package ratio_setting

import (
	"math"
	"testing"
)

func TestGPT56AndGPT6OfficialPricingRatios(t *testing.T) {
	tests := []struct {
		model            string
		inputPrice       float64
		cachedInputPrice float64
		outputPrice      float64
		modelRatio       float64
		completionRatio  float64
	}{
		{model: "gpt-6-astra", inputPrice: 10, cachedInputPrice: 1, outputPrice: 50, modelRatio: 5.0, completionRatio: 5},
		{model: "gpt-5.6-sol", inputPrice: 4, cachedInputPrice: 0.4, outputPrice: 20, modelRatio: 2.0, completionRatio: 5},
		{model: "gpt-5.6-terra", inputPrice: 2, cachedInputPrice: 0.2, outputPrice: 12, modelRatio: 1.0, completionRatio: 6},
		{model: "gpt-5.6-luna", inputPrice: 0.2, cachedInputPrice: 0.02, outputPrice: 1.2, modelRatio: 0.1, completionRatio: 6},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			modelRatio, ok := defaultModelRatio[tt.model]
			if modelRatio != tt.modelRatio {
				t.Fatalf("model ratio = %v, want %v", modelRatio, tt.modelRatio)
			}

			if completionRatio := GetCompletionRatio(tt.model); completionRatio != tt.completionRatio {
				t.Fatalf("completion ratio = %v, want %v", completionRatio, tt.completionRatio)
			}

			if got := modelRatio * 2; math.Abs(got-tt.inputPrice) > 1e-9 {
				t.Fatalf("input price = %v, want %v", got, tt.inputPrice)
			}
			if got := modelRatio * tt.completionRatio * 2; math.Abs(got-tt.outputPrice) > 1e-9 {
				t.Fatalf("output price = %v, want %v", got, tt.outputPrice)
			}

			cacheRatio, ok := defaultCacheRatio[tt.model]
			if !ok {
				t.Fatalf("expected cache ratio for %s", tt.model)
			}
			if got := modelRatio * cacheRatio * 2; math.Abs(got-tt.cachedInputPrice) > 1e-9 {
				t.Fatalf("cached input price = %v, want %v", got, tt.cachedInputPrice)
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
		{model: "deepseek-v4-flash", modelRatio: 1.0 / 1000 * RMB, completionRatio: 2, cacheRatio: 0.02 / 1.0},
		{model: "deepseek-v4-pro", modelRatio: 3.0 / 1000 * RMB, completionRatio: 2, cacheRatio: 0.025 / 3.0},
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
