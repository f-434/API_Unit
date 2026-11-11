package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"apiunit/internal/model"
	"apiunit/internal/modules/injection"
	"apiunit/internal/modules/tokenaudit"
	"apiunit/internal/requestdiff"
)

type stressExport struct {
	Agg     model.Aggregate        `json:"aggregate"`
	Models  []model.ModelAggregate `json:"models"`
	Planned int64                  `json:"planned"`
}

type exportData struct {
	Run         model.Run
	Headline    model.RunSummary
	Created     string
	Started     string
	Finished    string
	Duration    string
	ConfigJSON  string
	Items       []model.Item
	Stress      *stressExport
	Audit       *tokenaudit.Report
	RequestDiff *requestdiff.Report
	Injection   *injection.Report
}

func (s *Server) handleRunExport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	view, err := s.loadRunView(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "测试不存在")
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}
	switch format {
	case "json":
		s.exportJSON(w, r, view)
	case "html":
		s.exportHTML(w, view)
	case "csv":
		s.exportCSV(w, id)
	default:
		apiError(w, http.StatusBadRequest, "不支持的导出格式")
	}
}

func (s *Server) exportJSON(w http.ResponseWriter, r *http.Request, view *model.RunView) {
	payload := map[string]any{
		"run":        view.Run,
		"summary":    view.Summary,
		"config":     view.Config,
		"exportedAt": model.NowMs(),
	}
	if r.URL.Query().Get("items") == "1" {
		items, err := s.store.ListItemsAll(view.Run.ID, 50000, false)
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		details := make([]model.ItemDetail, len(items))
		for i, it := range items {
			details[i] = it.Detail()
		}
		payload["items"] = details
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="apiunit-%s.json"`, view.Run.ID))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func (s *Server) exportCSV(w http.ResponseWriter, id string) {
	items, err := s.store.ListItemsAll(id, 0, false)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="apiunit-%s-items.csv"`, id))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM for Excel
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"id", "index", "model", "target", "case", "category", "verdict", "ok", "status", "finish_reason",
		"latency_ms", "ttft_ms", "tpot_ms", "prompt_tokens", "completion_tokens",
		"total_tokens", "cost", "error_type", "error_message", "request_id", "started_at",
		"output_text", "request_json", "response_json",
	})
	for _, it := range items {
		_ = cw.Write([]string{
			strconv.FormatInt(it.ID, 10), strconv.Itoa(it.Index), it.Model, it.TargetName, it.CaseName, it.Category, it.Verdict,
			strconv.FormatBool(it.OK), strconv.Itoa(it.Status), it.FinishReason,
			fmt.Sprintf("%.2f", it.LatencyMs), fmt.Sprintf("%.2f", it.TTFTMs), fmt.Sprintf("%.2f", it.TPOTMs),
			strconv.Itoa(it.PromptTokens), strconv.Itoa(it.CompletionTokens), strconv.Itoa(it.TotalTokens),
			fmt.Sprintf("%.6f", it.Cost), it.ErrorType, it.ErrorMessage, it.RequestID,
			time.UnixMilli(it.StartedAt).Format(time.RFC3339),
			it.OutputText, it.RequestJSON, it.ResponseJSON,
		})
	}
}

func (s *Server) exportHTML(w http.ResponseWriter, view *model.RunView) {
	data := exportData{
		Run:      view.Run,
		Headline: view.Summary,
		Created:  formatMs(view.Run.CreatedAt),
		Started:  formatMs(view.Run.StartedAt),
		Finished: formatMs(view.Run.FinishedAt),
		Duration: durationText(view.Run.DurationMs),
	}
	if items, err := s.store.ListItemsAll(view.Run.ID, 50000, false); err == nil {
		data.Items = items
	}
	if view.Config != nil {
		if b, err := json.MarshalIndent(view.Config, "", "  "); err == nil {
			data.ConfigJSON = string(b)
		}
	}
	if view.Summary.Data != nil {
		raw, _ := json.Marshal(view.Summary.Data)
		switch view.Run.Kind {
		case model.KindStress:
			var se stressExport
			if json.Unmarshal(raw, &se) == nil {
				data.Stress = &se
			}
		case model.KindTokenAudit:
			var rep tokenaudit.Report
			if json.Unmarshal(raw, &rep) == nil {
				data.Audit = &rep
			}
		case model.KindRequestDiff:
			var rep requestdiff.Report
			if json.Unmarshal(raw, &rep) == nil {
				data.RequestDiff = &rep
				data.Audit = rep.Audit
			}
		case model.KindInjection:
			var rep injection.Report
			if json.Unmarshal(raw, &rep) == nil {
				data.Injection = &rep
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="apiunit-%s.html"`, view.Run.ID))
	_ = reportTemplate.Execute(w, data)
}

func formatMs(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func durationText(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"ms":  func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"pct": func(v float64) string { return fmt.Sprintf("%.2f%%", v) },
	"money": func(v float64) string {
		if v == 0 {
			return "-"
		}
		return fmt.Sprintf("$%.4f", v)
	},
	"num": func(v int64) string { return strconv.FormatInt(v, 10) },
	"kindLabel": func(k model.RunKind) string {
		switch k {
		case model.KindStress:
			return "并发压测"
		case model.KindTokenAudit:
			return "Token 审计"
		case model.KindRequestDiff:
			return "请求差异审计"
		case model.KindInjection:
			return "提示词注入安全测试"
		}
		return string(k)
	},
	"statusLabel": func(s model.RunStatus) string {
		switch s {
		case model.StatusCompleted:
			return "已完成"
		case model.StatusStopped:
			return "已停止"
		case model.StatusFailed:
			return "失败"
		case model.StatusRunning:
			return "运行中"
		case model.StatusInterrupted:
			return "已中断"
		}
		return string(s)
	},
	"verdictLabel": func(v string) string {
		switch v {
		case "success":
			return "注入成功"
		case "blocked":
			return "已防御"
		case "unclear":
			return "无法判定"
		case "error":
			return "执行失败"
		}
		return v
	},
	"severityLabel": func(s string) string {
		switch s {
		case "critical":
			return "严重"
		case "high":
			return "高"
		case "medium":
			return "中"
		case "low":
			return "低"
		}
		return s
	},
}).Parse(reportHTML))

const reportHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>API Unit 报告 {{.Run.ID}}</title>
<style>
:root{--ink:#1f2328;--muted:#6b7280;--line:#e5e7eb;--brand:#2563eb;--bad:#dc2626;--ok:#16a34a;--warn:#d97706}
*{box-sizing:border-box}body{margin:0;background:#f6f7f9;color:var(--ink);font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
.wrap{max-width:1180px;margin:0 auto;padding:28px 24px 60px}
header{background:#fff;border:1px solid var(--line);border-radius:12px;padding:22px 24px;margin-bottom:18px}
h1{margin:0 0 6px;font-size:22px}h2{font-size:17px;margin:26px 0 10px}h3{font-size:15px;margin:18px 0 8px}
.meta{display:flex;flex-wrap:wrap;gap:8px 20px;color:var(--muted);font-size:13px}
.tag{display:inline-block;padding:1px 8px;border-radius:999px;background:#eef2ff;color:#3730a3;font-size:12px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin:14px 0}
.card{background:#fff;border:1px solid var(--line);border-radius:10px;padding:12px 14px}
.card b{display:block;font-size:20px;margin-top:2px}.card span{color:var(--muted);font-size:12px}
section{background:#fff;border:1px solid var(--line);border-radius:12px;padding:6px 20px 18px;margin-bottom:18px}
table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:7px 8px;border-bottom:1px solid var(--line);text-align:left;vertical-align:top}
th{background:#fafafa;color:#374151;font-weight:600;white-space:nowrap}
.right{text-align:right}.mono{font-family:ui-monospace,Consolas,monospace}
.bad{color:var(--bad)}.ok{color:var(--ok)}.warn{color:var(--warn)}
.bar{height:8px;background:#eef2f7;border-radius:99px;overflow:hidden;min-width:80px}
.bar i{display:block;height:100%;background:var(--brand)}
.notes{color:var(--muted);font-size:12.5px;padding-left:18px}
details pre{white-space:pre-wrap;word-break:break-all;background:#0f172a;color:#e2e8f0;padding:14px;border-radius:10px;font-size:12px;max-height:520px;overflow:auto}
footer{color:var(--muted);text-align:center;font-size:12px;margin-top:28px}
@media print{body{background:#fff}section,header{break-inside:avoid}}
</style></head><body><div class="wrap">
<header>
<h1>API Unit 测试报告 <span class="tag">{{kindLabel .Run.Kind}}</span> <span class="tag">{{statusLabel .Run.Status}}</span></h1>
<div class="meta"><span>ID: <b class="mono">{{.Run.ID}}</b></span><span>创建: {{.Created}}</span><span>开始: {{.Started}}</span><span>结束: {{.Finished}}</span><span>耗时: {{.Duration}}</span></div>
</header>

<div class="cards">
<div class="card"><span>总请求</span><b>{{.Headline.Total}}</b></div>
<div class="card"><span>成功</span><b class="ok">{{.Headline.Success}}</b></div>
<div class="card"><span>失败</span><b class="bad">{{.Headline.Failed}}</b></div>
{{if eq (printf "%s" .Run.Kind) "stress"}}
<div class="card"><span>成功率</span><b>{{pct .Headline.SuccessRate}}</b></div>
<div class="card"><span>QPS</span><b>{{ms .Headline.QPS}}</b></div>
<div class="card"><span>RPM</span><b>{{ms .Headline.RPM}}</b></div>
{{end}}
{{if eq (printf "%s" .Run.Kind) "injection"}}
<div class="card"><span>ASR</span><b class="bad">{{pct .Headline.ASR}}</b></div>
{{end}}
<div class="card"><span>预估成本</span><b>{{money .Headline.Cost}}</b></div>
</div>

{{if .Stress}}
<section>
<h2>压测总览</h2>
<div class="cards">
<div class="card"><span>P50 延迟</span><b>{{ms .Stress.Agg.Latency.P50}} ms</b></div>
<div class="card"><span>P95 延迟</span><b>{{ms .Stress.Agg.Latency.P95}} ms</b></div>
<div class="card"><span>P99 延迟</span><b>{{ms .Stress.Agg.Latency.P99}} ms</b></div>
<div class="card"><span>平均 TTFT</span><b>{{ms .Stress.Agg.TTFT.Avg}} ms</b></div>
<div class="card"><span>输出 Token</span><b>{{num .Stress.Agg.Tokens.Completion}}</b></div>
<div class="card"><span>输出速度</span><b>{{ms .Stress.Agg.Tokens.AvgTPS}} tok/s</b></div>
</div>
<h3>模型对比</h3>
<table><tr><th>模型</th><th>协议</th><th class="right">请求</th><th class="right">成功率</th><th class="right">平均延迟</th><th class="right">P95</th><th class="right">TTFT</th><th class="right">QPS</th><th class="right">RPM</th><th class="right">Prompt</th><th class="right">Completion</th><th class="right">成本</th></tr>
{{range .Stress.Models}}<tr><td class="mono">{{.Model}}</td><td>{{.Protocol}}</td><td class="right">{{.Total}}</td><td class="right">{{pct .SuccessRate}}</td><td class="right">{{ms .Latency.Avg}}</td><td class="right">{{ms .Latency.P95}}</td><td class="right">{{ms .TTFT.Avg}}</td><td class="right">{{ms .QPS}}</td><td class="right">{{ms .RPM}}</td><td class="right">{{num .Tokens.Prompt}}</td><td class="right">{{num .Tokens.Completion}}</td><td class="right">{{money .Tokens.Cost}}</td></tr>{{end}}
</table>
{{if .Stress.Agg.Errors}}<h3>错误分组</h3>
<table><tr><th>类型</th><th class="right">次数</th><th>消息</th></tr>
{{range .Stress.Agg.Errors}}<tr><td>{{.Type}}</td><td class="right">{{.Count}}</td><td class="mono">{{.Message}}</td></tr>{{end}}</table>{{end}}
</section>
{{end}}

{{if .RequestDiff}}
<section>
<h2>请求差异审计</h2>
<div class="cards">
<div class="card"><span>请求快照</span><b>{{.RequestDiff.SnapshotCount}}</b></div>
<div class="card"><span>隐藏指令线索</span><b class="warn">{{.RequestDiff.FindingCount}}</b></div>
{{range $k, $v := .RequestDiff.FindingsBySeverity}}<div class="card"><span>线索级别 {{$k}}</span><b>{{$v}}</b></div>{{end}}
</div>
<h3>请求用例</h3>
<table><tr><th>用例</th><th class="right">消息数</th><th>历史</th><th>Tools</th><th>多模态</th><th>Web Search</th><th>Function Response</th></tr>
{{range .RequestDiff.Cases}}<tr><td>{{.Name}}</td><td class="right">{{.MessageCount}}</td><td>{{if .HasHistory}}是{{else}}-{{end}}</td><td>{{if .HasTools}}是{{else}}-{{end}}</td><td>{{if .HasMedia}}是{{else}}-{{end}}</td><td>{{if .HasWebSearch}}是{{else}}-{{end}}</td><td>{{if .HasFunctionResponse}}是{{else}}-{{end}}</td></tr>{{end}}
</table>
{{if .RequestDiff.FindingsByCategory}}<h3>线索分类</h3>
<table><tr><th>分类</th><th class="right">数量</th></tr>{{range $k, $v := .RequestDiff.FindingsByCategory}}<tr><td>{{$k}}</td><td class="right">{{$v}}</td></tr>{{end}}</table>{{end}}
<ul class="notes">{{range .RequestDiff.Notes}}<li>{{.}}</li>{{end}}</ul>
</section>
{{end}}

{{if .Audit}}
<section>
<h2>Token 审计</h2>
<div class="cards">
<div class="card"><span>总请求</span><b>{{.Audit.TotalRequests}}</b></div>
<div class="card"><span>成功</span><b class="ok">{{.Audit.Success}}</b></div>
<div class="card"><span>缺少 usage</span><b class="warn">{{.Audit.MissingUsage}}</b></div>
<div class="card"><span>异常</span><b class="bad">{{len .Audit.Anomalies}}</b></div>
<div class="card"><span>阈值</span><b>{{pct .Audit.Threshold}}</b></div>
</div>
<h3>目标汇总</h3>
<table><tr><th>目标</th><th>模型</th><th class="right">请求</th><th class="right">成功</th><th class="right">输入中位数</th><th class="right">本地 Token</th><th class="right">偏差</th><th class="right">异常数</th><th class="right">成本</th></tr>
{{range .Audit.Targets}}<tr><td>{{.TargetLabel}}</td><td class="mono">{{.Model}}</td><td class="right">{{.Count}}</td><td class="right">{{.Success}}</td><td class="right">{{ms .MedianInput}}</td><td class="right">{{ms .LocalTokens}}</td><td class="right">{{pct .OverheadPct}}</td><td class="right">{{.Anomalies}}</td><td class="right">{{money .Cost}}</td></tr>{{end}}
</table>
<h3>异常明细</h3>
{{if .Audit.Anomalies}}
<table><tr><th>提示词</th><th>目标</th><th>状态</th><th class="right">Delta</th><th class="right">波动</th><th class="right">本地偏差</th><th>原因</th></tr>
{{range .Audit.Anomalies}}<tr><td>{{.PromptName}}</td><td>{{.TargetLabel}} / {{.Model}}</td><td class="bad">{{.Status}}</td><td class="right">{{pct .DeltaPct}}</td><td class="right">{{pct .SpreadPct}}</td><td class="right">{{pct .OverheadPct}}</td><td>{{range .Reasons}}{{.}}；{{end}}</td></tr>{{end}}</table>
{{else}}<p class="ok">未发现超过阈值的异常。</p>{{end}}
<ul class="notes">{{range .Audit.Notes}}<li>{{.}}</li>{{end}}</ul>
</section>
{{end}}

{{if .Injection}}
<section>
<h2>提示词注入安全测试</h2>
<div class="cards">
<div class="card"><span>ASR</span><b class="bad">{{pct .Injection.Overall.ASR}}</b></div>
<div class="card"><span>注入成功</span><b class="bad">{{.Injection.Overall.Success}}</b></div>
<div class="card"><span>已防御</span><b class="ok">{{.Injection.Overall.Blocked}}</b></div>
<div class="card"><span>无法判定</span><b class="warn">{{.Injection.Overall.Unclear}}</b></div>
<div class="card"><span>执行失败</span><b>{{.Injection.Overall.Errors}}</b></div>
<div class="card"><span>裁判 Token</span><b>{{.Injection.JudgeTokens}}</b></div>
</div>
<h3>目标 ASR</h3>
<table><tr><th>目标</th><th>模型</th><th class="right">用例</th><th class="right">成功</th><th class="right">防御</th><th class="right">不确定</th><th class="right">ASR</th></tr>
{{range .Injection.Targets}}<tr><td>{{.TargetLabel}}</td><td class="mono">{{.Model}}</td><td class="right">{{.Total}}</td><td class="right">{{.Success}}</td><td class="right">{{.Blocked}}</td><td class="right">{{.Unclear}}</td><td class="right bad">{{pct .ASR}}</td></tr>{{end}}</table>
<h3>分类 ASR</h3>
<table><tr><th>分类</th><th class="right">用例</th><th class="right">成功</th><th class="right">防御</th><th class="right">ASR</th></tr>
{{range .Injection.Categories}}<tr><td>{{.Category}}</td><td class="right">{{.Total}}</td><td class="right">{{.Success}}</td><td class="right">{{.Blocked}}</td><td class="right bad">{{pct .ASR}}</td></tr>{{end}}</table>
<h3>用例明细</h3>
<table><tr><th>用例</th><th>分类</th><th>严重度</th><th class="right">总次数</th><th class="right">成功</th><th class="right">防御</th><th class="right">ASR</th></tr>
{{range .Injection.Cases}}<tr><td>{{.AttackName}}</td><td>{{.Category}}</td><td>{{severityLabel .Severity}}</td><td class="right">{{.Overall.Total}}</td><td class="right">{{.Overall.Success}}</td><td class="right">{{.Overall.Blocked}}</td><td class="right {{if gt .Overall.Success 0}}bad{{end}}">{{pct .Overall.ASR}}</td></tr>{{end}}</table>
<ul class="notes">{{range .Injection.Notes}}<li>{{.}}</li>{{end}}</ul>
</section>
{{end}}

{{if .Items}}
<section>
<h2>请求 / 响应明细</h2>
<p class="notes">共 {{len .Items}} 条。正文按落库内容展示；若条目提示“已截断”，说明超过了保存上限（请求 8MB / 响应 1MB / 输出 256KB）。</p>
{{range $i, $it := .Items}}
<details style="border-bottom:1px solid var(--line);padding:6px 0">
<summary><b>#{{$it.Index}}</b> <span class="mono">{{$it.Model}}</span> · HTTP {{$it.Status}} · {{if $it.OK}}<span class="ok">成功</span>{{else}}<span class="bad">失败</span>{{end}} · {{ms $it.LatencyMs}} ms{{if $it.FinishReason}} · finish={{$it.FinishReason}}{{end}}{{if index $it.Meta "truncated"}} · <span class="warn">已截断</span>{{end}}</summary>
<div class="meta" style="margin:6px 0">
<span>目标: {{$it.TargetName}}</span><span>协议: {{$it.Protocol}}</span><span>输入: {{$it.PromptTokens}}</span><span>输出: {{$it.CompletionTokens}}</span><span>TTFT: {{ms $it.TTFTMs}} ms</span>{{if $it.ErrorMessage}}<span class="bad">错误: {{$it.ErrorType}} {{$it.ErrorMessage}}</span>{{end}}
</div>
{{if $it.OutputText}}<h3>输出文本</h3><pre>{{$it.OutputText}}</pre>{{end}}
{{if $it.RequestJSON}}<h3>请求 JSON</h3><pre>{{$it.RequestJSON}}</pre>{{else}}<p class="warn">未保存请求正文（创建任务时未开启“保存请求/响应正文”）。</p>{{end}}
{{if $it.ResponseJSON}}<h3>响应 JSON</h3><pre>{{$it.ResponseJSON}}</pre>{{else}}<p class="warn">未保存响应正文（创建任务时未开启“保存请求/响应正文”）。</p>{{end}}
</details>
{{end}}
</section>
{{end}}

<section>
<h2>测试配置</h2>
<details><summary>展开查看原始配置</summary><pre>{{.ConfigJSON}}</pre></details>
</section>
<footer>Generated by API Unit · {{.Finished}}</footer>
</div></body></html>`
