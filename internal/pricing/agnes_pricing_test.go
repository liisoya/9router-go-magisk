package pricing

import "testing"

// Port of the pricing half of upstream
// tests/unit/agnes30-pro-model.test.js (decolua/9router#4587): the vendor's
// agnes-30-pro doc page publishes $0.45/$0.90 per 1M with cache read
// documented as 10% of input.
func TestGetPricingForModel_Agnes30(t *testing.T) {
	tests := []struct {
		model string
		want  ModelPricing
		found bool
	}{
		{
			model: "agnes-3.0-pro",
			want:  ModelPricing{InputPer1M: 0.45, OutputPer1M: 0.9, CachedPer1M: 0.045},
			found: true,
		},
		{
			// 3.0 Flash has no published price, so a fabricated rate would
			// mis-bill; it must stay absent.
			model: "agnes-3.0-flash",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, found := GetPricingForModel("agnes", tt.model)
			if found != tt.found {
				t.Fatalf("found = %v, want %v (got %+v)", found, tt.found, got)
			}
			if !tt.found {
				return
			}
			if got != tt.want {
				t.Errorf("pricing = %+v, want %+v", got, tt.want)
			}
			// The cached rate is the documented 10% of input, and the two
			// unpublished rates stay zero so CalculateCost falls back to
			// output/input the way upstream does.
			if diff := got.CachedPer1M - got.InputPer1M*0.1; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("cached = %v, want 10%% of input (%v)", got.CachedPer1M, got.InputPer1M*0.1)
			}
			if got.ReasoningPer1M != 0 || got.CacheCreationPer1M != 0 {
				t.Errorf("unpublished rates = %v/%v, want 0/0", got.ReasoningPer1M, got.CacheCreationPer1M)
			}
		})
	}
}
