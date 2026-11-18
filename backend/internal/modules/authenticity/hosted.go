package authenticity

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"apiunit/internal/model"
)

func (r *modelRun) runHosted() {
	profile := strings.ToLower(strings.TrimSpace(r.hosted.Profile))
	if profile == "" {
		profile = hostedProfileGeneric
	}
	if r.cfg.Hosted.Providers != nil {
		if v, ok := r.cfg.Hosted.Providers[string(r.protocolName)]; ok && !v {
			r.add(CheckReport{
				ID: "H0", Group: GroupHosted, Name: "工具调用检测已启用",
				Status: StatusNA, Weight: WeightSoft, Evidence: "当前协议未勾选工具调用",
			})
			return
		}
	}
	switch profile {
	case hostedProfileLegacy:
		switch r.protocolName {
		case model.ProtocolClaude:
			r.hostedLegacyClaude()
		case model.ProtocolGemini:
			r.hostedLegacyGemini()
		default:
			r.hostedLegacyOpenAI()
		}
	default:
		switch r.protocolName {
		case model.ProtocolClaude:
			r.hostedGenericClaude()
		case model.ProtocolGemini:
			r.hostedGenericGemini()
		default:
			r.hostedGenericOpenAI()
		}
	}
}

func (r *modelRun) hostedGenericOpenAI() {
	if strings.TrimSpace(r.hosted.MCP.ServerURL) == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "MCP 已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 MCP serverUrl"})
		return
	}
	endpoint, err := resolveEndpoint(r.job.target, r.job.spec, model.ProtocolOpenAI, r.job.model, "responses")
	if err != nil {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "Responses 端点解析", Status: StatusFail, Weight: WeightSoft, Evidence: err.Error()})
		return
	}
	headers := authHeaders(r.job.target, r.job.spec, model.ProtocolOpenAI, endpoint)
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	mcpHeaders := make(map[string]string, len(r.hosted.MCP.Headers)+2)
	for k, v := range r.hosted.MCP.Headers {
		mcpHeaders[k] = v
	}
	// The checker generates the per-run secret; the MCP server receives it in
	// a header (the model cannot see headers) and must echo it in the tool
	// result. This preserves the nonce contract without hard-coded credentials.
	mcpHeaders["X-Proof-Nonce"] = r.challenge.Nonce
	mcpHeaders["X-Proof-Secret"] = r.challenge.Secret
	tools := []any{map[string]any{
		"type":               "mcp",
		"server_label":       r.hosted.MCP.ServerLabel,
		"server_description": r.hosted.MCP.ServerDescription,
		"server_url":         r.hosted.MCP.ServerURL,
		"headers":            mcpHeaders,
		"require_approval":   r.hosted.MCP.RequireApproval,
	}}
	if domain != "" {
		tools = append(tools, map[string]any{
			"type":    "web_search",
			"filters": map[string]any{"allowed_domains": []string{domain}},
		})
	}
	prompt := fmt.Sprintf(
		"调用 MCP 工具 %s 获取本次校验记录，并%s。最终只输出一个 JSON："+
			`{"challenge":%q,"secret":"<MCP 返回的 secret>","found":true}`,
		r.hosted.MCP.ToolName, searchClause(r.proofTarget(domain)), r.challenge.Nonce)
	body := map[string]any{
		"model":       r.job.model,
		"input":       prompt,
		"tools":       tools,
		"tool_choice": "required",
		"include":     []string{"web_search_call.action.sources"},
	}
	res := r.postWithRetry(r.ctx, endpoint, headers, body, r.hostedTimeout())
	if res.Status == 400 && strings.Contains(strings.ToLower(string(res.Body)), "tool_choice") {
		body["tool_choice"] = "auto"
		res = r.postWithRetry(r.ctx, endpoint, headers, body, r.hostedTimeout())
	}
	r.observe(res)
	parsed := parseResponses(res.Body)
	r.captureResponses(parsed)

	raw := string(res.Body)
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "实际调用了 MCP 工具",
			Status: boolStatus(len(parsed.MCPCalls) > 0, "未检测到 mcp_call", "检测到 mcp_call"),
			Weight: WeightHard, Evidence: fmt.Sprintf("mcp_call=%d", len(parsed.MCPCalls)), CaseID: "hosted.openai",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "MCP 返回包含本次证明数据",
			Status: boolStatus(strings.Contains(raw, r.challenge.Secret) || strings.Contains(raw, r.challenge.Nonce),
				"响应中未找到本次 nonce/secret", "响应中包含本次 nonce/secret"),
			Weight: WeightHard, Evidence: "校验 MCP 输出/最终文本是否包含本次随机值", CaseID: "hosted.openai",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "最终答案消费了 MCP 结果",
			Status: boolStatus(strings.Contains(r.lastText, r.challenge.Secret), "最终文本未回显 secret", "最终文本回显 secret"),
			Weight: WeightHard, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.openai",
		},
	}
	if domain != "" {
		checks = append(checks,
			CheckReport{
				ID: "H5", Group: GroupHosted, Name: "实际调用了 web_search",
				Status: boolStatus(len(parsed.WebCalls) > 0, "未检测到 web_search_call", "检测到 web_search_call"),
				Weight: WeightSoft, Evidence: fmt.Sprintf("web_search_call=%d", len(parsed.WebCalls)), CaseID: "hosted.openai",
			},
			CheckReport{
				ID: "H6", Group: GroupHosted, Name: "搜索结果命中证明域名",
				Status: boolStatus(strings.Contains(raw, domain), "未找到证明域名", "命中 "+domain),
				Weight: WeightSoft, Evidence: "域名=" + domain, CaseID: "hosted.openai",
			},
		)
	}
	r.add(checks...)
	r.record(res, "hosted.openai", "工具调用：OpenAI Responses MCP", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedLegacyOpenAI() {
	if strings.TrimSpace(r.hosted.MCP.ServerURL) == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "MCP 已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 MCP serverUrl"})
		return
	}
	endpoint, err := resolveEndpoint(r.job.target, r.job.spec, model.ProtocolOpenAI, r.job.model, "responses")
	if err != nil {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "Responses 端点解析", Status: StatusFail, Weight: WeightSoft, Evidence: err.Error()})
		return
	}
	headers := authHeaders(r.job.target, r.job.spec, model.ProtocolOpenAI, endpoint)
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	if domain == "" {
		domain = strings.TrimSpace(r.hosted.Legacy.Domain)
	}
	tools := []any{map[string]any{
		"type":               "mcp",
		"server_label":       r.hosted.MCP.ServerLabel,
		"server_description": r.hosted.MCP.ServerDescription,
		"server_url":         r.hosted.MCP.ServerURL,
		"headers":            r.hosted.MCP.Headers,
		"require_approval":   r.hosted.MCP.RequireApproval,
	}}
	if domain != "" {
		tools = append(tools, map[string]any{
			"type":    "web_search",
			"filters": map[string]any{"allowed_domains": []string{domain}},
		})
	}
	prompt := r.legacyOpenAIPrompt(domain)
	body := map[string]any{
		"model":       r.job.model,
		"input":       prompt,
		"tools":       tools,
		"tool_choice": "required",
		"include":     []string{"web_search_call.action.sources"},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "website_users_environment_data",
				"strict": true,
				"schema": legacyOpenAISchema(),
			},
		},
	}
	res := r.postWithRetry(r.ctx, endpoint, headers, body, r.hostedTimeout())
	if res.Status == 400 && strings.Contains(strings.ToLower(string(res.Body)), "tool_choice") {
		body["tool_choice"] = "auto"
		res = r.postWithRetry(r.ctx, endpoint, headers, body, r.hostedTimeout())
	}
	r.observe(res)
	parsed := parseResponses(res.Body)
	r.captureResponses(parsed)

	final, _, _ := decodeLooseJSON(r.lastText)
	env := getMap(final, "model_authenticity_status")
	users := getMap(final, "users_data")
	record := getMap(users, "record")
	if record == nil && users != nil {
		if s := getString(users, "record"); s != "" {
			record = decodeObject([]byte(s))
		}
	}
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "实际调用了 MCP 工具",
			Status: boolStatus(len(parsed.MCPCalls) > 0, "未检测到 mcp_call", "检测到 mcp_call"),
			Weight: WeightHard, Evidence: fmt.Sprintf("mcp_call=%d", len(parsed.MCPCalls)), CaseID: "hosted.openai.legacy",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "最终结果包含关键字",
			Status: boolStatus(containsFold(r.lastText, r.hosted.Legacy.Keyword), "未包含 "+r.hosted.Legacy.Keyword, "包含 "+r.hosted.Legacy.Keyword),
			Weight: WeightSoft, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.openai.legacy",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "环境变量为期望值",
			Status: boolStatus(env != nil && getBool(env, "found") && getString(env, "value") == r.hosted.Legacy.ExpectedEnvValue,
				"未读取到期望的环境变量", "环境变量匹配"),
			Weight: WeightHard, Evidence: fmt.Sprintf("found=%v value=%q", getBool(env, "found"), getString(env, "value")), CaseID: "hosted.openai.legacy",
		},
		{
			ID: "H5", Group: GroupHosted, Name: "MCP 数据查询成功",
			Status: boolStatus(users != nil && getBool(users, "query_success"), "query_success 不为 true", "query_success=true"),
			Weight: WeightHard, Evidence: fmt.Sprintf("query_success=%v error=%q", getBool(users, "query_success"), getString(users, "error")), CaseID: "hosted.openai.legacy",
		},
	}
	recordOK := true
	for k, want := range r.hosted.Legacy.ExpectedRecord {
		if got := record[k]; fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			recordOK = false
		}
	}
	checks = append(checks, CheckReport{
		ID: "H6", Group: GroupHosted, Name: "MCP 记录字段符合预期",
		Status: boolStatus(recordOK && record != nil, "记录字段不匹配", "记录字段匹配"),
		Weight: WeightHard, Evidence: fmt.Sprintf("record=%s", truncateText(jsonPretty(record), 300)), CaseID: "hosted.openai.legacy",
	})
	r.add(checks...)
	r.record(res, "hosted.openai.legacy", "工具调用：OpenAI Responses 兼容 MCP", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedGenericClaude() {
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	if domain == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "证明页已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 proof domain"})
		return
	}
	prompt := fmt.Sprintf("请使用 web_search 搜索并读取 %s 的证明页，提取其中的 secret。最终只输出 JSON：%s",
		r.proofTarget(domain), fmt.Sprintf(`{"challenge":%q,"secret":"<证明页中的 secret>"}`, r.challenge.Nonce))
	body := map[string]any{
		"model":      r.job.model,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": prompt},
		},
		"tools": []any{map[string]any{
			"type":            claudeWebSearchType(r.job.model),
			"name":            "web_search",
			"allowed_domains": []string{domain},
			"max_uses":        3,
		}},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.hostedTimeout())
	r.observe(res)
	parsed := parseClaude(res.Body)
	r.captureClaude(parsed)
	search, encrypted, urls := claudeSearchInfo(res.Body)
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "实际调用了 web_search",
			Status: boolStatus(search, "未检测到 web_search_tool_result", "检测到 web_search_tool_result"),
			Weight: WeightHard, Evidence: fmt.Sprintf("results_urls=%v", urls), CaseID: "hosted.claude",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "搜索结果包含证明域名",
			Status: boolStatus(containsFold(fmt.Sprint(urls), domain) || strings.Contains(string(res.Body), domain),
				"未找到 "+domain, "命中 "+domain),
			Weight: WeightSoft, Evidence: fmt.Sprintf("urls=%v", urls), CaseID: "hosted.claude",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "搜索结果含 encrypted_content",
			Status: boolStatus(encrypted, "未检测到 encrypted_content", "检测到 encrypted_content"),
			Weight: WeightHard, Evidence: fmt.Sprintf("encrypted=%t", encrypted), CaseID: "hosted.claude",
		},
		{
			ID: "H5", Group: GroupHosted, Name: "最终答案回显本次挑战码",
			Status: boolStatus(strings.Contains(r.lastText, r.challenge.Nonce), "最终文本未回显 challenge", "最终文本回显 challenge"),
			Weight: WeightHard, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.claude",
		},
	}
	r.add(checks...)
	r.record(res, "hosted.claude", "工具调用：Claude web_search", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedLegacyClaude() {
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	if domain == "" {
		domain = strings.TrimSpace(r.hosted.Legacy.Domain)
	}
	if domain == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "证明域名已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 proof/legacy domain"})
		return
	}
	prompt := fmt.Sprintf("请使用 web_search 搜索 %s 的主站内容，并使用 xlsx Skill 创建一个 Excel 文件；最终回答中必须包含 %s。",
		domain, r.hosted.Legacy.Keyword)
	body := map[string]any{
		"model":      r.job.model,
		"max_tokens": 2048,
		"messages": []any{
			map[string]any{"role": "user", "content": prompt},
		},
		"container": map[string]any{
			"skills": []any{map[string]any{
				"type": "anthropic", "skill_id": "xlsx", "version": "latest",
			}},
		},
		"tools": []any{
			map[string]any{
				"type":            claudeWebSearchType(r.job.model),
				"name":            "web_search",
				"allowed_domains": []string{domain},
				"max_uses":        3,
			},
			map[string]any{
				"type": "code_execution_20260120",
				"name": "code_execution",
			},
		},
	}
	headers := cloneHeaders(r.headers)
	headers["anthropic-beta"] = joinBeta(headers["anthropic-beta"], "code-execution-2025-08-25")
	res := r.postWithRetry(r.ctx, r.endpoint, headers, body, r.hostedTimeout())
	r.observe(res)
	parsed := parseClaude(res.Body)
	r.captureClaude(parsed)
	search, encrypted, urls := claudeSearchInfo(res.Body)
	containerOK, skillOK, codeOK := claudeSkillInfo(res.Body)
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "实际调用了 web_search",
			Status: boolStatus(search, "未检测到 web_search_tool_result", "检测到 web_search_tool_result"),
			Weight: WeightHard, Evidence: fmt.Sprintf("urls=%v", urls), CaseID: "hosted.claude.legacy",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "搜索结果包含证明域名",
			Status: boolStatus(containsFold(fmt.Sprint(urls), domain) || strings.Contains(string(res.Body), domain),
				"未找到 "+domain, "命中 "+domain),
			Weight: WeightSoft, Evidence: fmt.Sprintf("urls=%v", urls), CaseID: "hosted.claude.legacy",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "搜索结果含 encrypted_content",
			Status: boolStatus(encrypted, "未检测到 encrypted_content", "检测到 encrypted_content"),
			Weight: WeightHard, Evidence: fmt.Sprintf("encrypted=%t", encrypted), CaseID: "hosted.claude.legacy",
		},
		{
			ID: "H5", Group: GroupHosted, Name: "最终结果包含关键字",
			Status: boolStatus(containsFold(r.lastText, r.hosted.Legacy.Keyword), "未包含 "+r.hosted.Legacy.Keyword, "包含 "+r.hosted.Legacy.Keyword),
			Weight: WeightSoft, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.claude.legacy",
		},
		{
			ID: "H6", Group: GroupHosted, Name: "container 实际加载 xlsx Skill",
			Status: boolStatus(containerOK && skillOK, "container/skills 未加载 xlsx", "container xlsx Skill 已加载"),
			Weight: WeightHard, Evidence: "container + skills 检查", CaseID: "hosted.claude.legacy",
		},
		{
			ID: "H7", Group: GroupHosted, Name: "使用了代码执行 / Skills 工具",
			Status: boolStatus(codeOK, "未检测到代码执行工具或结果块", "检测到代码执行工具及结果"),
			Weight: WeightHard, Evidence: "server_tool_use / code result 检查", CaseID: "hosted.claude.legacy",
		},
	}
	r.add(checks...)
	r.record(res, "hosted.claude.legacy", "工具调用：Claude 兼容 web_search + Skills", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedGenericGemini() {
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	if domain == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "证明页已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 proof domain"})
		return
	}
	prompt := fmt.Sprintf("请使用 google_search 搜索 %s 的证明页，提取 secret。最终只输出 JSON：%s",
		r.proofTarget(domain), fmt.Sprintf(`{"challenge":%q,"secret":"<证明页中的 secret>"}`, r.challenge.Nonce))
	body := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": prompt}},
		}},
		"tools": []any{map[string]any{"googleSearch": map[string]any{}}},
		"generationConfig": map[string]any{
			"maxOutputTokens": 1024,
			"temperature":     0,
		},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.hostedTimeout())
	r.observe(res)
	parsed := parseGemini(res.Body)
	r.captureGemini(parsed)
	grounded := len(parsed.Grounding) > 0 || len(parsed.WebQueries) > 0
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "grounding 表明实际联网搜索",
			Status: boolStatus(grounded, "未检测到 groundingMetadata", "检测到 groundingMetadata"),
			Weight: WeightHard, Evidence: fmt.Sprintf("queries=%v grounding=%d", parsed.WebQueries, len(parsed.Grounding)), CaseID: "hosted.gemini",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "搜索结果命中证明域名",
			Status: boolStatus(strings.Contains(string(res.Body), domain), "未找到 "+domain, "命中 "+domain),
			Weight: WeightSoft, Evidence: "domain=" + domain, CaseID: "hosted.gemini",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "最终答案回显本次挑战码",
			Status: boolStatus(strings.Contains(r.lastText, r.challenge.Nonce), "最终文本未回显 challenge", "最终文本回显 challenge"),
			Weight: WeightHard, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.gemini",
		},
	}
	r.add(checks...)
	r.record(res, "hosted.gemini", "工具调用：Gemini google_search", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedLegacyGemini() {
	domain := strings.TrimSpace(r.hosted.Proof.Domain)
	if domain == "" {
		domain = strings.TrimSpace(r.hosted.Legacy.Domain)
	}
	if domain == "" {
		r.add(CheckReport{ID: "H0", Group: GroupHosted, Name: "证明域名已配置", Status: StatusNA, Weight: WeightSoft, Evidence: "未配置 proof/legacy domain"})
		return
	}
	prompt := fmt.Sprintf("请使用 google_search 搜索 %s 的主站内容，最终回答中必须包含 %s。", domain, r.hosted.Legacy.Keyword)
	body := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": prompt}},
		}},
		"tools": []any{map[string]any{"googleSearch": map[string]any{}}},
		"generationConfig": map[string]any{
			"maxOutputTokens": 1024,
			"temperature":     0,
		},
	}
	res := r.postWithRetry(r.ctx, r.endpoint, r.headers, body, r.hostedTimeout())
	r.observe(res)
	parsed := parseGemini(res.Body)
	r.captureGemini(parsed)
	grounded := len(parsed.Grounding) > 0 || len(parsed.WebQueries) > 0
	checks := []CheckReport{
		hostedHTTPCheck("H1", "工具调用请求返回 HTTP 200", res),
		{
			ID: "H2", Group: GroupHosted, Name: "grounding 表明实际联网搜索",
			Status: boolStatus(grounded, "未检测到 groundingMetadata", "检测到 groundingMetadata"),
			Weight: WeightHard, Evidence: fmt.Sprintf("queries=%v", parsed.WebQueries), CaseID: "hosted.gemini.legacy",
		},
		{
			ID: "H3", Group: GroupHosted, Name: "搜索结果命中证明域名",
			Status: boolStatus(strings.Contains(string(res.Body), domain), "未找到 "+domain, "命中 "+domain),
			Weight: WeightSoft, Evidence: "domain=" + domain, CaseID: "hosted.gemini.legacy",
		},
		{
			ID: "H4", Group: GroupHosted, Name: "最终结果包含关键字",
			Status: boolStatus(containsFold(r.lastText, r.hosted.Legacy.Keyword), "未包含 "+r.hosted.Legacy.Keyword, "包含 "+r.hosted.Legacy.Keyword),
			Weight: WeightSoft, Evidence: truncateText(r.lastText, 240), CaseID: "hosted.gemini.legacy",
		},
	}
	r.add(checks...)
	r.record(res, "hosted.gemini.legacy", "工具调用：Gemini 兼容 google_search", GroupHosted, r.lastText, checks...)
}

func (r *modelRun) hostedTimeout() time.Duration {
	d := time.Duration(r.cfg.Hosted.TimeoutMs) * time.Millisecond
	if d <= 0 {
		d = 5 * time.Minute
	}
	return d
}

func (r *modelRun) captureResponses(res responsesResult) {
	if res.ID != "" {
		r.lastID = res.ID
	}
	if res.Model != "" {
		r.lastModel = res.Model
	}
	if res.Status != "" {
		r.lastFinish = res.Status
	}
	if res.OutputText != "" {
		r.lastText = res.OutputText
	}
	if res.Usage.Reported {
		r.lastUsage = res.Usage
	}
}

func hostedHTTPCheck(id, name string, res httpResult) CheckReport {
	status := StatusPass
	evidence := fmt.Sprintf("HTTP %d", res.Status)
	if res.Err != nil {
		status = StatusFail
		evidence = "网络错误: " + res.Err.Error()
	} else if !res.statusOK() {
		status = StatusFail
		evidence += " " + truncateText(strings.TrimSpace(string(res.Body)), 300)
	}
	return CheckReport{ID: id, Group: GroupHosted, Name: name, Status: status, Weight: WeightHard, Evidence: evidence}
}

func searchClause(domain string) string {
	if domain == "" {
		return "只依据 MCP 结果作答"
	}
	return "搜索 " + domain + " 的证明页"
}

func (r *modelRun) proofURL() string {
	base := strings.TrimSpace(r.hosted.Proof.BaseURL)
	if base == "" {
		return ""
	}
	path := strings.TrimSpace(r.hosted.Proof.PathTemplate)
	if path == "" {
		path = "/proof/{runToken}"
	}
	return strings.TrimRight(base, "/") + strings.ReplaceAll(path, "{runToken}", r.challenge.Nonce)
}

func (r *modelRun) proofTarget(domain string) string {
	if u := r.proofURL(); u != "" {
		return u
	}
	return domain
}

func claudeWebSearchType(modelName string) string {
	if claudeAdaptive(modelName) {
		return "web_search_20260209"
	}
	return "web_search_20250305"
}

func claudeSearchInfo(body []byte) (found, encrypted bool, urls []string) {
	var payload struct {
		Content []struct {
			Type    string `json:"type"`
			Content []struct {
				Type             string `json:"type"`
				URL              string `json:"url"`
				EncryptedContent string `json:"encrypted_content"`
			} `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false, false, nil
	}
	for _, block := range payload.Content {
		if block.Type != "web_search_tool_result" {
			continue
		}
		found = true
		for _, item := range block.Content {
			if item.URL != "" {
				urls = append(urls, item.URL)
			}
			if len(item.EncryptedContent) >= 32 {
				encrypted = true
			}
		}
	}
	return
}

func claudeSkillInfo(body []byte) (containerOK, skillOK, codeOK bool) {
	var payload struct {
		Container struct {
			ID     string `json:"id"`
			Skills []struct {
				Type    string `json:"type"`
				SkillID string `json:"skill_id"`
				Version string `json:"version"`
			} `json:"skills"`
		} `json:"container"`
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false, false, false
	}
	containerOK = payload.Container.ID != ""
	for _, s := range payload.Container.Skills {
		if strings.EqualFold(s.SkillID, "xlsx") {
			skillOK = true
		}
	}
	for _, block := range payload.Content {
		switch block.Type {
		case "server_tool_use":
			if block.Name == "code_execution" || block.Name == "bash_code_execution" || block.Name == "text_editor_code_execution" {
				codeOK = true
			}
		case "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
			codeOK = true
		}
	}
	return containerOK, skillOK, codeOK
}

func (r *modelRun) legacyOpenAIPrompt(domain string) string {
	return fmt.Sprintf(`请严格执行以下任务，最终只输出 JSON。
任务一：网页信息。仅搜索和访问 %s 主站，提取网站名称、地址和介绍；无法确认的字段留空，不得编造。
任务二：数据库查询。调用 MCP 工具查询 %s 集合的第一条记录，查询成功时 query_success=true，并将记录序列化为 JSON 字符串放入 record。
任务三：读取环境变量。调用 MCP 工具读取名称严格等于 %s 的环境变量，只有实际读取到值时才 found=true。
执行要求：必须实际调用网页搜索和 MCP；不得根据工具名称猜测结果；只读操作。`,
		fallbackString(domain, "证明站点"),
		fallbackString(r.hosted.Legacy.Collection, "users"),
		fallbackString(r.hosted.Legacy.EnvVar, "MODEL_AUTHENTICITY_STATUS"))
}

func legacyOpenAISchema() map[string]any {
	str := map[string]any{"type": "string"}
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type": "object", "properties": props,
			"required": required, "additionalProperties": false,
		}
	}
	return obj(map[string]any{
		"website_information": obj(map[string]any{
			"website_name": str, "url": str, "description": str,
		}, "website_name", "url", "description"),
		"users_data": obj(map[string]any{
			"collection_name": str,
			"query_success":   map[string]any{"type": "boolean"},
			"record":          str,
			"error":           str,
		}, "collection_name", "query_success", "record", "error"),
		"model_authenticity_status": obj(map[string]any{
			"variable_name": str,
			"found":         map[string]any{"type": "boolean"},
			"value":         str,
			"error":         str,
		}, "variable_name", "found", "value", "error"),
	}, "website_information", "users_data", "model_authenticity_status")
}

func fallbackString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
