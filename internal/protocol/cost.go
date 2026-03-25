package protocol

import "strings"

type modelPricing struct {
	prefix     string
	input      float64 // per MTok
	output     float64
	cacheWrite float64
	cacheRead  float64
}

// Longest prefix first to avoid short-prefix false matches.
var pricingTable = []modelPricing{
	{"claude-opus-4-5", 5.00, 25.00, 6.25, 0.50},
	{"claude-opus-4-6", 5.00, 25.00, 6.25, 0.50},
	{"claude-opus-4", 15.00, 75.00, 18.75, 1.50},
	{"claude-sonnet-4", 3.00, 15.00, 3.75, 0.30},
	{"claude-haiku-4-5", 1.00, 5.00, 1.25, 0.10},
	{"claude-haiku-3-5", 0.80, 4.00, 1.00, 0.08},
}

// Default fallback: sonnet pricing.
var defaultPricing = modelPricing{"", 3.00, 15.00, 3.75, 0.30}

// CalculateCost returns the estimated dollar cost for the given token usage.
// The API's input_tokens includes cache_creation + cache_read, so we subtract
// those to get the uncached input tokens billed at the regular input rate.
func CalculateCost(model string, input, output, cacheWrite, cacheRead int64) float64 {
	p := defaultPricing
	for _, mp := range pricingTable {
		if strings.HasPrefix(model, mp.prefix) {
			p = mp
			break
		}
	}

	uncachedInput := input - cacheWrite - cacheRead
	if uncachedInput < 0 {
		uncachedInput = 0
	}

	cost := (float64(uncachedInput)*p.input +
		float64(output)*p.output +
		float64(cacheWrite)*p.cacheWrite +
		float64(cacheRead)*p.cacheRead) / 1_000_000

	return cost
}
