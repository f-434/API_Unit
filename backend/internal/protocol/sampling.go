package protocol

import "strings"

// 部分新模型已移除采样参数（temperature / top_p / top_k），上游会返回
// 400/422 并明确指出参数名，例如：
//
//	{"type":"error","error":{"type":"invalid_request_error",
//	 "message":"`temperature` is deprecated for this model."}}
//
// 下面的识别逻辑只处理“参数本身不被接受”这一类错误，避免把
// “参数取值非法”（例如 temperature 超出范围）误判成兼容性问题。

const (
	ParamTemperature = "temperature"
	ParamTopP        = "top_p"
	ParamTopK        = "top_k"
)

// samplingParamAliases 列出上游错误信息中可能出现的参数拼写。
var samplingParamAliases = []struct {
	Param   string
	Aliases []string
}{
	{ParamTemperature, []string{"temperature"}},
	{ParamTopP, []string{"top_p", "top-p", "topp"}},
	{ParamTopK, []string{"top_k", "top-k", "topk"}},
}

// unsupportedPhrases 是“该参数不被支持或已废弃”的常见措辞。
var unsupportedPhrases = []string{
	"deprecated", "not support", "unsupported", "does not support", "doesn't support",
	"not allowed", "not accepted", "no longer support", "removed",
	"不支持", "已废弃", "不再支持", "不可用",
}

// RejectedSamplingParams 判断响应是否是“当前模型不接受某个采样参数”的
// 显式拒绝，返回被拒绝的参数规范名。
//
// 仅识别 400/422 响应：这类错误发生在生成任何内容之前，重试是安全的；
// 2xx 流中途报错时重试可能造成重复生成/计费，交由原有的重试策略处理。
func RejectedSamplingParams(out *Outcome) []string {
	if out == nil || out.OK || out.ErrorMessage == "" {
		return nil
	}
	if out.Status != 400 && out.Status != 422 {
		return nil
	}
	msg := strings.ToLower(out.ErrorMessage)
	var rejected []string
	for _, item := range samplingParamAliases {
		for _, alias := range item.Aliases {
			if mentionsUnsupported(msg, alias) {
				rejected = append(rejected, item.Param)
				break
			}
		}
	}
	return rejected
}

// mentionsUnsupported 判断参数名与“不支持/已废弃”措辞是否出现在同一处。
// 窗口取 80 字节，既能覆盖前后修饰语，也不至于跨句误伤。
func mentionsUnsupported(msg, alias string) bool {
	const window = 80
	for offset := 0; offset < len(msg); {
		i := aliasIndex(msg, alias, offset)
		if i < 0 {
			return false
		}
		start := i - window
		if start < 0 {
			start = 0
		}
		end := i + len(alias) + window
		if end > len(msg) {
			end = len(msg)
		}
		segment := msg[start:end]
		for _, phrase := range unsupportedPhrases {
			if strings.Contains(segment, phrase) {
				return true
			}
		}
		offset = i + len(alias)
	}
	return false
}

// aliasIndex 查找 alias 在 msg 中从 from 开始的首次出现位置，要求两侧
// 不是英文字母，避免把 stopped 里的 "topp" 误当成 top_p。
func aliasIndex(msg, alias string, from int) int {
	for from < len(msg) {
		i := strings.Index(msg[from:], alias)
		if i < 0 {
			return -1
		}
		i += from
		beforeOK := i == 0 || !isASCIILetter(msg[i-1])
		after := i + len(alias)
		afterOK := after >= len(msg) || !isASCIILetter(msg[after])
		if beforeOK && afterOK {
			return i
		}
		from = i + 1
	}
	return -1
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// StripSamplingParams 从请求中移除指定的采样参数，返回实际移除的参数名。
// 除结构体字段外还会清理 ExtraGeneration / ExtraBody 中的同名字段，
// 因为部分模块通过扩展字段透传采样参数。
func StripSamplingParams(req *ChatRequest, params []string) []string {
	if req == nil || len(params) == 0 {
		return nil
	}
	removed := map[string]bool{}
	for _, param := range params {
		switch param {
		case ParamTemperature:
			if req.Temperature != nil {
				req.Temperature = nil
				removed[param] = true
			}
		case ParamTopP:
			if req.TopP != nil {
				req.TopP = nil
				removed[param] = true
			}
		case ParamTopK:
			if req.TopK != nil {
				req.TopK = nil
				removed[param] = true
			}
		}
	}
	for _, extra := range []map[string]any{req.ExtraGeneration, req.ExtraBody} {
		for key := range extra {
			for _, param := range params {
				if extraKeyMatchesParam(key, param) {
					delete(extra, key)
					removed[param] = true
				}
			}
		}
	}
	out := make([]string, 0, len(removed))
	for _, param := range params {
		if removed[param] {
			out = append(out, param)
		}
	}
	return out
}

// extraKeyMatchesParam 忽略大小写与分隔符比较扩展字段名，
// 例如 topP / top_p / TOP-P 都视为 top_p。
func extraKeyMatchesParam(key, param string) bool {
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, "-", "")
		return s
	}
	switch param {
	case ParamTemperature:
		return norm(key) == "temperature"
	case ParamTopP:
		return norm(key) == "topp"
	case ParamTopK:
		return norm(key) == "topk"
	}
	return false
}
