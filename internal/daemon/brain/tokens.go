package brain

// EstimateTokens gives a rough token count (~4 chars per token).
func EstimateTokens(text string) int {
	return len(text) / 4
}

// TruncateToTokens truncates text to approximately maxTokens.
func TruncateToTokens(text string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "\n... (truncated)"
}
