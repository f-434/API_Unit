package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"time"

	"apiunit/internal/model"
	"apiunit/internal/requestdiff"
)

type diffExportData struct {
	Mode      string
	Left      *model.SnapshotDetail
	Right     *model.SnapshotDetail
	Result    *requestdiff.Result
	LeftBody  string
	RightBody string
	LeftTime  string
	RightTime string
	Generated string
}

func renderDiffHTML(w io.Writer, left, right *model.SnapshotDetail, mode string, result *requestdiff.Result) {
	data := diffExportData{
		Mode: mode, Left: left, Right: right, Result: result,
		LeftBody: prettyJSON(left.Body), RightBody: prettyJSON(right.Body),
		LeftTime: formatMs(left.RequestTime), RightTime: formatMs(right.RequestTime),
		Generated: time.Now().Format("2006-01-02 15:04:05"),
	}
	if mode == "canonical" {
		data.LeftBody = prettyJSON(left.Canonical)
		data.RightBody = prettyJSON(right.Canonical)
	}
	_ = diffTemplate.Execute(w, data)
}

func prettyJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}

var diffTemplate = template.Must(template.New("diff").Funcs(template.FuncMap{
	"val": func(v any) string {
		if v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	},
	"opLabel": func(s string) string {
		switch s {
		case "add":
			return "新增"
		case "remove":
			return "删除"
		case "replace":
			return "替换"
		}
		return s
	},
	"catLabel": func(s string) string {
		m := map[string]string{
			"system_prompt": "System Prompt", "user_prompt": "User Prompt", "history": "历史对话",
			"tools": "Tools/Functions", "function_response": "Function Response", "generation_config": "生成参数",
			"safety_settings": "安全设置", "cached_content": "缓存内容", "media": "图片/音视频/文件",
			"web_search": "Web Search", "other": "其他",
		}
		if v, ok := m[s]; ok {
			return v
		}
		return s
	},
	"sevLabel": func(s string) string {
		switch s {
		case "high":
			return "高"
		case "medium":
			return "中"
		case "low":
			return "低"
		}
		return s
	},
	"changed": func(b bool) string {
		if b {
			return "已变化"
		}
		return "一致"
	},
}).Parse(diffHTML))

const diffHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>API Unit 请求差异报告</title>
<style>
:root{--ink:#1f2328;--muted:#6b7280;--line:#e5e7eb;--brand:#2563eb;--bad:#dc2626;--ok:#16a34a;--warn:#d97706}
*{box-sizing:border-box}body{margin:0;background:#f6f7f9;color:var(--ink);font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif}
.wrap{max-width:1280px;margin:0 auto;padding:26px 22px 60px}
header,section{background:#fff;border:1px solid var(--line);border-radius:12px;padding:6px 20px 18px;margin-bottom:16px}
header{padding:20px}h1{margin:0 0 6px;font-size:22px}h2{font-size:17px;margin:20px 0 10px}
.meta{display:flex;flex-wrap:wrap;gap:6px 18px;color:var(--muted);font-size:13px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:12px;margin:14px 0}
.card{border:1px solid var(--line);border-radius:10px;padding:10px 14px}.card b{display:block;font-size:20px}.card span{color:var(--muted);font-size:12px}
table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:7px 8px;border-bottom:1px solid var(--line);text-align:left;vertical-align:top}
th{background:#fafafa;white-space:nowrap}.mono{font-family:ui-monospace,Consolas,monospace}
pre{white-space:pre-wrap;word-break:break-all;background:#0f172a;color:#e2e8f0;padding:14px;border-radius:10px;font-size:12px;max-height:620px;overflow:auto}
.ok{color:var(--ok)}.bad{color:var(--bad)}.warn{color:var(--warn)}
.two{display:grid;grid-template-columns:1fr 1fr;gap:16px}@media(max-width:900px){.two{grid-template-columns:1fr}}
</style></head><body><div class="wrap">
<header>
<h1>API Unit 请求差异报告</h1>
<div class="meta"><span>对比模式：<b>{{if eq .Mode "canonical"}}canonical{{else}}wire（协议原生）{{end}}</b></span>
<span>左侧 #{{.Left.ID}} · {{.Left.TargetLabel}} · {{.Left.Model}} · {{.Left.CaseName}} · {{.LeftTime}}</span>
<span>右侧 #{{.Right.ID}} · {{.Right.TargetLabel}} · {{.Right.Model}} · {{.Right.CaseName}} · {{.RightTime}}</span></div>
<div class="cards">
<div class="card"><span>差异总数</span><b>{{.Result.Summary.Total}}</b></div>
<div class="card"><span>新增</span><b class="ok">{{.Result.Summary.Added}}</b></div>
<div class="card"><span>删除</span><b class="bad">{{.Result.Summary.Removed}}</b></div>
<div class="card"><span>替换</span><b class="warn">{{.Result.Summary.Replaced}}</b></div>
<div class="card"><span>左 SHA-256</span><b class="mono" style="font-size:11px">{{.Result.LeftSHA}}</b></div>
<div class="card"><span>右 SHA-256</span><b class="mono" style="font-size:11px">{{.Result.RightSHA}}</b></div>
</div>
</header>

<section>
<h2>关键分区指纹</h2>
<table><tr><th>分区</th><th>左侧 SHA-256</th><th>右侧 SHA-256</th><th>结果</th></tr>
{{range .Result.Sections}}<tr><td>{{.Label}}</td><td class="mono">{{if .Left}}{{.Left}}{{else}}-{{end}}</td><td class="mono">{{if .Right}}{{.Right}}{{else}}-{{end}}</td><td class="{{if .Changed}}bad{{else}}ok{{end}}">{{changed .Changed}}</td></tr>{{end}}
</table>
</section>

<section>
<h2>差异明细</h2>
<table><tr><th>操作</th><th>JSONPath</th><th>分类</th><th>级别</th><th>修改前</th><th>修改后</th></tr>
{{range .Result.Changes}}<tr><td>{{opLabel .Op}}</td><td class="mono">{{.Path}}</td><td>{{catLabel .Category}}</td><td>{{sevLabel .Severity}}</td><td><pre style="max-height:220px">{{val .Before}}</pre></td><td><pre style="max-height:220px">{{val .After}}</pre></td></tr>{{end}}
{{if not .Result.Changes}}<tr><td colspan="6" class="ok">两侧请求完全一致。</td></tr>{{end}}
</table>
</section>

<section><h2>左侧请求 JSON</h2><pre>{{.LeftBody}}</pre></section>
<section><h2>右侧请求 JSON</h2><pre>{{.RightBody}}</pre></section>
<p class="meta">Generated by API Unit · {{.Generated}}</p>
</div></body></html>`
