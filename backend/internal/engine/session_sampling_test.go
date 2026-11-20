package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"apiunit/internal/model"
	"apiunit/internal/protocol"
)

// TestExecDropsRejectedSamplingParam 验证：上游以“temperature 已废弃”拒绝时，
// 引擎会移除该参数后自动重试，并把结论缓存到本次运行，后续请求直接省略。
func TestExecDropsRejectedSamplingParam(t *testing.T) {
	var calls int32
	var withTemperature int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"temperature"`) {
			atomic.AddInt32(&withTemperature, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"` + "`temperature`" + ` is deprecated for this model."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-sonnet-5-5","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`))
	}))
	defer srv.Close()

	sess := NewSession("r_test", model.KindTokenAudit, nil, protocol.NewClient(), nil, nil)
	temp := 0.0
	req := ExecRequest{
		Target: protocol.TargetInfo{
			ID: "t_test", Name: "test", BaseURL: srv.URL, APIKey: "k",
			Protocol: model.ProtocolClaude,
		},
		Model:       "claude-sonnet-5-5",
		Protocol:    model.ProtocolClaude,
		Messages:    []protocol.ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens:   16,
		Temperature: &temp,
		Timeout:     5 * time.Second,
	}

	it, ok := sess.Exec(context.Background(), req)
	if !ok {
		t.Fatal("请求不应被取消")
	}
	if !it.OK {
		t.Fatalf("期望更正后成功，实际失败: %s", it.ErrorMessage)
	}
	if got := it.Meta["droppedParams"]; !reflect.DeepEqual(got, []string{protocol.ParamTemperature}) {
		t.Fatalf("droppedParams = %v, want [temperature]", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("首次请求应经历 1 次更正，共 2 次调用，实际 %d 次", got)
	}

	// 第二次请求应复用兼容性缓存，直接省略 temperature，只发一次。
	if _, ok := sess.Exec(context.Background(), req); !ok {
		t.Fatal("第二次请求不应被取消")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("第二次请求应直接省略 temperature，累计 3 次调用，实际 %d 次", got)
	}
	if got := atomic.LoadInt32(&withTemperature); got != 1 {
		t.Fatalf("只有首次请求会携带 temperature，实际 %d 次", got)
	}
}
