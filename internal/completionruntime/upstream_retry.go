package completionruntime

import (
	"context"
	"io"
	"net/http"
	"strings"

	"ds2api/internal/assistantturn"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
)

// IsTransientUpstreamStatus reports whether an upstream HTTP status represents a
// temporary failure that may succeed on a retry or an alternate account.
func IsTransientUpstreamStatus(status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// transientUpstreamError converts a transient upstream failure into the
// retryable upstream_unavailable code so the shared retry machinery can switch
// accounts instead of surfacing the raw error. It returns nil when the status is
// not transient.
func transientUpstreamError(status int, message string) *assistantturn.OutputError {
	if !IsTransientUpstreamStatus(status) {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(status)
	}
	return &assistantturn.OutputError{Status: status, Message: message, Code: "upstream_unavailable"}
}

// transientUpstreamResponseError promotes a transient non-200 upstream response
// into a retryable error, consuming and closing the response body. It returns nil
// when the status is not transient, leaving the response untouched for callers
// that surface non-transient upstream errors verbatim.
func transientUpstreamResponseError(resp *http.Response) *assistantturn.OutputError {
	if resp == nil || !IsTransientUpstreamStatus(resp.StatusCode) {
		return nil
	}
	message := ""
	if resp.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			config.Logger.Warn("[completion_runtime] transient response body read failed", "status", resp.StatusCode, "error", readErr)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			config.Logger.Warn("[completion_runtime] transient response body close failed", "status", resp.StatusCode, "error", closeErr)
		}
		message = strings.TrimSpace(string(body))
	}
	return transientUpstreamError(resp.StatusCode, message)
}

// promoteTransientStartResponse upgrades a StartResult that carries a transient
// non-200 upstream response into a retryable error. It is a no-op when the start
// already failed or returned a non-transient response.
func promoteTransientStartResponse(start *StartResult, outErr *assistantturn.OutputError) *assistantturn.OutputError {
	if outErr != nil || start == nil || start.Response == nil {
		return outErr
	}
	transientErr := transientUpstreamResponseError(start.Response)
	if transientErr == nil {
		return nil
	}
	start.Response = nil
	return transientErr
}

// completionCallOutputError classifies a transport error returned by
// CallCompletion. Caller cancellations and muted accounts keep their existing
// semantics; any other transport failure is treated as transient upstream
// unavailability so the retry machinery can switch accounts instead of passing
// the raw error downstream.
func completionCallOutputError(ctx context.Context, err error) *assistantturn.OutputError {
	if dsclient.IsMutedError(err) {
		return &assistantturn.OutputError{Status: http.StatusForbidden, Message: "Account is muted by upstream.", Code: "account_muted"}
	}
	if ctx != nil && ctx.Err() != nil {
		return &assistantturn.OutputError{Status: http.StatusInternalServerError, Message: "Failed to get completion.", Code: "error"}
	}
	return &assistantturn.OutputError{Status: http.StatusServiceUnavailable, Message: "Failed to get completion.", Code: "upstream_unavailable"}
}

// transientUpstreamErrorKeywords mark upstream error messages that describe a
// temporary condition worth retrying on another account. Permanent failures such
// as over-long input are intentionally absent.
var transientUpstreamErrorKeywords = []string{
	"暂时",
	"稍后",
	"请重试",
	"繁忙",
	"超时",
	"temporarily",
	"try again",
	"unavailable",
	"timeout",
	"overloaded",
	"busy",
}

// IsTransientUpstreamErrorMessage reports whether an upstream error message looks
// like a temporary failure. Only messages that hit a known transient keyword are
// treated as retryable; everything else keeps the existing non-retryable
// upstream_error behavior.
func IsTransientUpstreamErrorMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	for _, keyword := range transientUpstreamErrorKeywords {
		if strings.Contains(normalized, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// isRetryableUpstreamError reports whether an upstream_error OutputError carries
// a transient message that justifies switching accounts.
func isRetryableUpstreamError(outErr *assistantturn.OutputError) bool {
	return outErr != nil && outErr.Code == "upstream_error" && IsTransientUpstreamErrorMessage(outErr.Message)
}
