package daemon

import (
	"errors"
	"net"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// classifyError determines if an API error is retryable and returns a
// user-friendly description.
func classifyError(err error) (retryable bool, friendlyMsg string) {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 429:
			return true, "Rate limited by API"
		case 500:
			return true, "API internal server error"
		case 502:
			return true, "API bad gateway"
		case 503:
			return true, "API temporarily unavailable"
		case 529:
			return true, "API overloaded"
		case 401:
			return false, "Invalid API key"
		case 400:
			return false, "Bad request"
		case 403:
			return false, "Permission denied"
		case 404:
			return false, "Model not found"
		default:
			if apiErr.StatusCode >= 500 {
				return true, "API server error"
			}
			return false, "API error"
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true, "Network error"
	}

	msg := err.Error()
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF") {
		return true, "Connection lost"
	}

	return false, "Unexpected error"
}
