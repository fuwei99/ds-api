package client

import (
	"errors"
	"fmt"
)

type FailureKind string

// errAllDeviceIDsInvalid 是 manual 模式下号池里最后一个 device_id 也被风控
// 判定失效后返回的错误。此时无法再换号重试，只能如实上报给调用方。
var errAllDeviceIDsInvalid = errors.New("所有device_id均已失效")

const (
	FailureUnknown             FailureKind = ""
	FailureDirectUnauthorized  FailureKind = "direct_unauthorized"
	FailureManagedUnauthorized FailureKind = "managed_unauthorized"
	FailureCaptchaRequired     FailureKind = "captcha_required"
	FailureMuted               FailureKind = "account_muted"
)

type RequestFailure struct {
	Op      string
	Kind    FailureKind
	Message string
}

func (e *RequestFailure) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Op != "" && e.Message != "":
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	case e.Op != "":
		return e.Op + " failed"
	case e.Message != "":
		return e.Message
	default:
		return "request failed"
	}
}

func IsManagedUnauthorizedError(err error) bool {
	var failure *RequestFailure
	return errors.As(err, &failure) && failure.Kind == FailureManagedUnauthorized
}

func IsDirectUnauthorizedError(err error) bool {
	var failure *RequestFailure
	return errors.As(err, &failure) && failure.Kind == FailureDirectUnauthorized
}

func IsMutedError(err error) bool {
	var failure *RequestFailure
	return errors.As(err, &failure) && failure.Kind == FailureMuted
}
