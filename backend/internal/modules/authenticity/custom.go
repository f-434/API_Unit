package authenticity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"apiunit/internal/protocol"
)

func (r *modelRun) runCustomProbes() {
	for _, p := range r.cfg.CustomProbes {
		if p.Protocol != "" && p.Protocol != "auto" && p.Protocol != string(r.protocolName) {
			continue
		}
		endpoint, err := r.customEndpoint(p)
		if err != nil {
			r.add(CheckReport{
				ID: "C-" + p.ID, Group: GroupCustom, Name: p.Name,
				Status: StatusFail, Weight: WeightSoft, Evidence: "端点解析失败: " + err.Error(),
			})
			continue
		}
		headers := cloneHeaders(r.headers)
		for k, v := range p.Headers {
			headers[k] = v
		}
		var body any
		if p.Method != "GET" {
			body = p.Body
		}
		res := r.do(r.ctx, p.Method, endpoint, headers, body, r.timeout())
		checks := r.evaluateCustom(p, res)
		if len(checks) == 0 {
			status := StatusPass
			evidence := fmt.Sprintf("HTTP %d", res.Status)
			if res.Err != nil {
				status = StatusFail
				evidence = "网络错误: " + res.Err.Error()
			} else if !res.statusOK() {
				status = StatusFail
			}
			checks = append(checks, CheckReport{
				ID: "C-" + p.ID, Group: GroupCustom, Name: p.Name,
				Status: status, Weight: WeightSoft, Evidence: evidence,
			})
		}
		r.add(checks...)
		r.record(res, "custom."+p.ID, p.Name, GroupCustom, string(res.Body), checks...)
	}
}

func (r *modelRun) customEndpoint(p CustomProbe) (string, error) {
	raw := strings.ReplaceAll(p.Path, "{model}", r.job.model)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw, nil
	}
	root, err := protocol.NormalizeBaseURL(r.job.target.BaseURL)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(root, "/") + "/" + strings.TrimLeft(raw, "/"), nil
}

func (r *modelRun) evaluateCustom(p CustomProbe, res httpResult) []CheckReport {
	out := make([]CheckReport, 0, len(p.Expected))
	for i, e := range p.Expected {
		id := fmt.Sprintf("C-%s-%d", p.ID, i+1)
		name := fmt.Sprintf("%s：%s", p.Name, e.Type)
		status := StatusFail
		evidence := ""
		switch e.Type {
		case "status":
			want, err := strconv.Atoi(strings.TrimSpace(e.Value))
			if err != nil {
				evidence = "期望值不是整数: " + e.Value
				break
			}
			if res.Status == want {
				status = StatusPass
			}
			evidence = fmt.Sprintf("期望 HTTP %d，实际 HTTP %d", want, res.Status)
		case "json_path":
			var root any
			if err := json.Unmarshal(res.Body, &root); err != nil {
				evidence = "响应不是合法 JSON: " + err.Error()
				break
			}
			got := lookupJSONPath(root, e.Path)
			actual := fmt.Sprintf("%v", got)
			if actual == e.Value {
				status = StatusPass
			}
			evidence = fmt.Sprintf("path=%s 期望=%q 实际=%q", e.Path, e.Value, actual)
		case "contains":
			if strings.Contains(string(res.Body), e.Value) {
				status = StatusPass
			}
			evidence = fmt.Sprintf("期望响应包含 %q", e.Value)
		case "regex":
			re, err := regexp.Compile(e.Value)
			if err != nil {
				evidence = "正则无效: " + err.Error()
				break
			}
			if re.Match(res.Body) {
				status = StatusPass
			}
			evidence = fmt.Sprintf("正则=%q", e.Value)
		case "header":
			actual := res.Header.Get(e.Path)
			if actual == e.Value {
				status = StatusPass
			}
			evidence = fmt.Sprintf("header=%s 期望=%q 实际=%q", e.Path, e.Value, actual)
		default:
			evidence = "未知期望类型: " + e.Type
		}
		out = append(out, CheckReport{
			ID: id, Group: GroupCustom, Name: name, Status: status,
			Weight: WeightSoft, Evidence: evidence, CaseID: "custom." + p.ID,
			DurationMs: int64(res.LatencyMs),
		})
	}
	return out
}

func lookupJSONPath(root any, path string) any {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return root
	}
	cur := root
	for _, part := range strings.Split(p, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx, err := strconv.Atoi(part); err == nil {
			list, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(list) {
				return nil
			}
			cur = list[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

var _ = http.MethodGet
