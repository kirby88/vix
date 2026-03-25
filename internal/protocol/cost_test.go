package protocol

import (
	"math"
	"testing"
)

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		input      int64
		output     int64
		cacheWrite int64
		cacheRead  int64
		wantMin    float64 // use range to handle float precision
		wantMax    float64
	}{
		{
			name:    "sonnet pricing",
			model:   "claude-sonnet-4-20250514",
			input:   1_000_000,
			output:  100_000,
			wantMin: 4.49, wantMax: 4.51, // 1M*3/1M + 100k*15/1M = 3.0 + 1.5 = 4.5
		},
		{
			name:    "opus 4.5 pricing",
			model:   "claude-opus-4-5-20250929",
			input:   1_000_000,
			output:  100_000,
			wantMin: 7.49, wantMax: 7.51, // 1M*5/1M + 100k*25/1M = 5.0 + 2.5 = 7.5
		},
		{
			name:    "opus 4.6 pricing",
			model:   "claude-opus-4-6-20250514",
			input:   1_000_000,
			output:  100_000,
			wantMin: 7.49, wantMax: 7.51, // same as opus-4-5
		},
		{
			name:    "opus 4 pricing (expensive)",
			model:   "claude-opus-4-20250514",
			input:   1_000_000,
			output:  100_000,
			wantMin: 22.49, wantMax: 22.51, // 1M*15/1M + 100k*75/1M = 15.0 + 7.5 = 22.5
		},
		{
			name:    "haiku pricing",
			model:   "claude-haiku-4-5-20251001",
			input:   1_000_000,
			output:  100_000,
			wantMin: 1.49, wantMax: 1.51, // 1M*1/1M + 100k*5/1M = 1.0 + 0.5 = 1.5
		},
		{
			name:    "unknown model uses sonnet fallback",
			model:   "unknown-model",
			input:   1_000_000,
			output:  100_000,
			wantMin: 4.49, wantMax: 4.51,
		},
		{
			name:       "cache deduction clamps to zero",
			model:      "claude-sonnet-4-20250514",
			input:      100,
			output:     0,
			cacheWrite: 80,
			cacheRead:  80,
			// uncachedInput = 100 - 80 - 80 = -60, clamped to 0
			// cost = 0 + 0 + 80*3.75/1M + 80*0.30/1M ≈ 0.000324
			wantMin: 0.0003, wantMax: 0.0004,
		},
		{
			name:    "zero tokens",
			model:   "claude-sonnet-4-20250514",
			input:   0,
			output:  0,
			wantMin: 0, wantMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateCost(tt.model, tt.input, tt.output, tt.cacheWrite, tt.cacheRead)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("CalculateCost() = %f, want [%f, %f]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCalculateCostPrefixOrdering(t *testing.T) {
	// Verify that "claude-opus-4-5" matches before "claude-opus-4"
	// (longest prefix first in table)
	costOpus45 := CalculateCost("claude-opus-4-5-20250929", 1_000_000, 0, 0, 0)
	costOpus4 := CalculateCost("claude-opus-4-20250514", 1_000_000, 0, 0, 0)

	if math.Abs(costOpus45-5.0) > 0.01 {
		t.Errorf("opus-4-5 cost = %f, want ~5.0 (not opus-4 pricing)", costOpus45)
	}
	if math.Abs(costOpus4-15.0) > 0.01 {
		t.Errorf("opus-4 cost = %f, want ~15.0", costOpus4)
	}
}
