package protocol

import (
	"reflect"
	"testing"
)

func TestRejectedSamplingParams(t *testing.T) {
	cases := []struct {
		name string
		out  Outcome
		want []string
	}{
		{
			name: "Claude temperature 已废弃",
			out:  Outcome{Status: 400, ErrorMessage: "`temperature` is deprecated for this model."},
			want: []string{ParamTemperature},
		},
		{
			name: "OpenAI 不支持的参数",
			out:  Outcome{Status: 400, ErrorMessage: "Unsupported parameter: 'top_p' is not supported with this model."},
			want: []string{ParamTopP},
		},
		{
			name: "中文提示",
			out:  Outcome{Status: 400, ErrorMessage: "该模型不支持 temperature 参数"},
			want: []string{ParamTemperature},
		},
		{
			name: "同时拒绝多个参数",
			out:  Outcome{Status: 422, ErrorMessage: "temperature and top_p are not supported for this model"},
			want: []string{ParamTemperature, ParamTopP},
		},
		{
			name: "top-k 拼写",
			out:  Outcome{Status: 400, ErrorMessage: "`top-k` is deprecated for this model."},
			want: []string{ParamTopK},
		},
		{
			name: "取值非法不应当作不兼容",
			out:  Outcome{Status: 400, ErrorMessage: "temperature must be between 0 and 2"},
			want: nil,
		},
		{
			name: "max_tokens 废弃与采样参数无关",
			out:  Outcome{Status: 400, ErrorMessage: "max_tokens is deprecated for this model."},
			want: nil,
		},
		{
			name: "服务端错误不更正",
			out:  Outcome{Status: 500, ErrorMessage: "unsupported parameter: temperature"},
			want: nil,
		},
		{
			name: "成功响应不处理",
			out:  Outcome{Status: 200, OK: true, ErrorMessage: "`temperature` is deprecated"},
			want: nil,
		},
	}
	for _, c := range cases {
		if got := RejectedSamplingParams(&c.out); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStripSamplingParams(t *testing.T) {
	temp, topP, topK := 0.2, 0.9, 40
	req := ChatRequest{
		Temperature:     &temp,
		TopP:            &topP,
		TopK:            &topK,
		ExtraGeneration: map[string]any{"temperature": 0.3, "max_tokens": 10},
		ExtraBody:       map[string]any{"topP": 0.8, "seed": 1},
	}
	got := StripSamplingParams(&req, []string{ParamTemperature, ParamTopP, ParamTopK})
	want := []string{ParamTemperature, ParamTopP, ParamTopK}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removed = %v, want %v", got, want)
	}
	if req.Temperature != nil || req.TopP != nil || req.TopK != nil {
		t.Fatal("采样参数字段应被清空")
	}
	if _, ok := req.ExtraGeneration["temperature"]; ok {
		t.Fatal("ExtraGeneration 中的 temperature 应被移除")
	}
	if _, ok := req.ExtraBody["topP"]; ok {
		t.Fatal("ExtraBody 中的 topP 应被移除")
	}
	if _, ok := req.ExtraGeneration["max_tokens"]; !ok {
		t.Fatal("无关字段不应被移除")
	}
	if _, ok := req.ExtraBody["seed"]; !ok {
		t.Fatal("无关字段不应被移除")
	}
}
