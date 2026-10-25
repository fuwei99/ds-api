package history

import (
	"context"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/promptcompat"
)

const reminderHead = "The only correct format for using tools is <|EPSE|tool_calls>"

func reminderStandardRequest() promptcompat.StandardRequest {
	req := markerStandardRequest("")
	req.ToolMarker = ""
	return req
}

// TestApplyCurrentInputFileRebuildKeepsBottomFormatInjectionOff 固定 P0 不变量：
// input_file 续写会用新的消息序列重建 FinalPrompt，重建必须沿用请求上的
// 「底部格式提示注入」开关，否则关闭的开关会在续写这一跳被悄悄重新打开。
func TestApplyCurrentInputFileRebuildKeepsBottomFormatInjectionOff(t *testing.T) {
	uploader := &recordingUploader{}
	svc := Service{Store: enabledCurrentInputConfig{minChars: 1}, DS: uploader}

	stdReq := reminderStandardRequest()
	stdReq.ToolReminderDisabled = true
	out, err := svc.ApplyCurrentInputFile(context.Background(), &auth.RequestAuth{}, stdReq)
	if err != nil {
		t.Fatalf("ApplyCurrentInputFile: %v", err)
	}
	if !out.CurrentInputFileApplied {
		t.Fatal("expected the current input file split to be applied")
	}
	if strings.Contains(out.FinalPrompt, reminderHead) {
		t.Fatalf("disabled injection must survive the continuation rebuild, got=%q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "工具调用格式规范") {
		t.Fatalf("the format spec must still be injected, got=%q", out.FinalPrompt)
	}
}

// TestApplyCurrentInputFileRebuildUsesCustomBottomFormatPrompt 验证续写重建会
// 使用请求上的自定义提醒文本。
func TestApplyCurrentInputFileRebuildUsesCustomBottomFormatPrompt(t *testing.T) {
	uploader := &recordingUploader{}
	svc := Service{Store: enabledCurrentInputConfig{minChars: 1}, DS: uploader}

	stdReq := reminderStandardRequest()
	stdReq.ToolReminderPrompt = "自定义底部格式提醒"
	out, err := svc.ApplyCurrentInputFile(context.Background(), &auth.RequestAuth{}, stdReq)
	if err != nil {
		t.Fatalf("ApplyCurrentInputFile: %v", err)
	}
	if !strings.HasSuffix(out.FinalPrompt, "自定义底部格式提醒[Assistant]:") {
		t.Fatalf("custom reminder must stay immediately before the [Assistant] marker, got=%q", out.FinalPrompt)
	}
	if strings.Contains(out.FinalPrompt, reminderHead) {
		t.Fatalf("custom reminder must replace the built-in one, got=%q", out.FinalPrompt)
	}
}
