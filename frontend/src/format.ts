import dayjs from 'dayjs'
import type { AuditReport } from './types'

export const fmtInt = (v?: number) => (v === undefined || v === null || Number.isNaN(v) ? '-' : new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(v))
export const fmtNum = (v?: number, digits = 2) => (v === undefined || v === null || Number.isNaN(v) ? '-' : new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits, minimumFractionDigits: 0 }).format(v))
export const fmtPct = (v?: number, digits = 2) => (v === undefined || v === null || Number.isNaN(v) ? '-' : `${Number(v).toFixed(digits)}%`)
export const fmtMs = (v?: number) => (v === undefined || v === null || Number.isNaN(v) ? '-' : `${Number(v).toFixed(v >= 100 ? 0 : 1)} ms`)
export const fmtCost = (v?: number, currency = '$') => {
  if (!v) return '-'
  if (v < 0.000001) return `< ${currency}0.000001`
  return `${currency}${v.toFixed(v >= 1 ? 4 : 6)}`
}

// Audit success rate = normal cells / total cells (one cell per prompt × target).
export const auditCellStats = (report?: AuditReport) => {
  if (!report || !report.prompts?.length) return { total: 0, normal: 0, rate: undefined as number | undefined }
  let total = 0
  let normal = 0
  for (const row of report.prompts) {
    for (const c of row.cells || []) {
      total++
      if (c.status === 'normal') normal++
    }
  }
  return { total, normal, rate: total ? (normal / total) * 100 : undefined }
}
export const targetAuditStats = (report: AuditReport | undefined, targetIndex: number) => {
  let total = 0
  let normal = 0
  for (const row of report?.prompts || []) {
    for (const c of row.cells || []) {
      if (c.targetIndex !== targetIndex) continue
      total++
      if (c.status === 'normal') normal++
    }
  }
  return { total, normal, rate: total ? (normal / total) * 100 : undefined }
}
export const requestSuccessRate = (report?: AuditReport) =>
  report && report.totalRequests > 0 ? (report.success / report.totalRequests) * 100 : undefined
export const fmtTime = (ms?: number) => (!ms ? '-' : dayjs(ms).format('YYYY-MM-DD HH:mm:ss'))
export const fmtClock = (ms?: number) => (!ms ? '-' : dayjs(ms).format('HH:mm:ss'))
export const fmtBytes = (n?: number) => {
  if (n === undefined || n === null) return '-'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}
export const fmtDuration = (ms?: number) => {
  if (!ms || ms < 0) return '-'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${(ms / 1000).toFixed(1)}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}m`
}
export const fmtShortId = (id?: string) => (id ? id.length > 14 ? `${id.slice(0, 6)}…${id.slice(-4)}` : id : '-')

export const kindLabel = (k?: string) => {
  switch (k) {
    case 'stress': return '并发压测'
    case 'token_audit': return 'Token 审计'
    case 'request_diff': return '请求差异审计'
    case 'injection': return '提示词注入'
    default: return k || '-'
  }
}

export const kindColor = (k?: string) => {
  switch (k) {
    case 'stress': return 'blue'
    case 'token_audit': return 'gold'
    case 'request_diff': return 'purple'
    case 'injection': return 'red'
    default: return 'default'
  }
}

export const statusLabel = (s?: string) => {
  switch (s) {
    case 'queued': return '排队中'
    case 'running': return '运行中'
    case 'completed': return '已完成'
    case 'stopped': return '已停止'
    case 'failed': return '失败'
    case 'interrupted': return '已中断'
    default: return s || '-'
  }
}
export const statusColor = (s?: string) => {
  switch (s) {
    case 'running': return 'processing'
    case 'completed': return 'success'
    case 'stopped': return 'warning'
    case 'failed': return 'error'
    case 'interrupted': return 'default'
    default: return 'default'
  }
}

export const verdictLabel = (v?: string) => {
  switch (v) {
    case 'success': return '注入成功'
    case 'blocked': return '已防御'
    case 'unclear': return '无法判定'
    case 'error': return '执行失败'
    case 'normal': return '正常'
    case 'inflation': return 'Token 膨胀'
    case 'unstable': return '波动异常'
    case 'no_usage': return '缺少 usage'
    case 'warning': return '需关注'
    default: return v || '-'
  }
}
export const verdictColor = (v?: string) => {
  switch (v) {
    case 'success': return 'error'
    case 'blocked': return 'success'
    case 'unclear': return 'warning'
    case 'error': return 'default'
    case 'normal': return 'success'
    case 'inflation': return 'error'
    case 'unstable': return 'warning'
    case 'no_usage': return 'warning'
    case 'warning': return 'warning'
    default: return 'default'
  }
}

export const severityLabel = (s?: string) => {
  switch (s) {
    case 'critical': return '严重'
    case 'high': return '高'
    case 'medium': return '中'
    case 'low': return '低'
    default: return s || '-'
  }
}
export const severityColor = (s?: string) => {
  switch (s) {
    case 'critical': return 'magenta'
    case 'high': return 'red'
    case 'medium': return 'orange'
    case 'low': return 'blue'
    default: return 'default'
  }
}

export const categoryLabel = (c?: string) => {
  const map: Record<string, string> = {
    system_prompt_leak: '系统提示词泄露',
    instruction_override: '指令覆盖',
    jailbreak: '越狱',
    data_exfiltration: '数据外泄',
    encoding_bypass: '编码绕过',
    role_play: '角色扮演绕过',
    indirect_injection: '间接注入',
    tool_abuse: '工具/插件滥用',
    system_prompt: 'System Prompt',
    user_prompt: 'User Prompt',
    history: '历史对话',
    tools: 'Tools / Functions',
    function_response: 'Function Response',
    generation_config: '生成参数',
    safety_settings: '安全设置',
    cached_content: '缓存内容',
    media: '图片/音视频/文件',
    web_search: 'Web Search',
    other: '其他',
    system: 'System Prompt',
    user: 'User Prompt',
    functionResponse: 'Function Response',
    generation: '生成参数',
    safety: '安全设置',
    cached: '缓存内容',
    webSearch: 'Web Search',
  }
  return (c && map[c]) || c || '-'
}

export const opLabel = (op?: string) => {
  switch (op) {
    case 'add': return '新增'
    case 'remove': return '删除'
    case 'replace': return '替换'
    default: return op || '-'
  }
}
export const opColor = (op?: string) => {
  switch (op) {
    case 'add': return 'green'
    case 'remove': return 'red'
    case 'replace': return 'orange'
    default: return 'default'
  }
}

export const tokenSourceLabel = (s?: string) => {
  switch (s) {
    case 'usage': return 'usage'
    case 'mixed': return '部分估算'
    case 'estimated': return '本地估算'
    case 'none': return '无'
    default: return s || '-'
  }
}

export const protocolLabel = (p?: string) => {
  switch (p) {
    case 'openai': return 'OpenAI'
    case 'claude': return 'Claude'
    case 'gemini': return 'Gemini'
    case 'auto': return '自动'
    default: return p || '自动'
  }
}

export const errorTypeLabel = (t?: string) => {
  const map: Record<string, string> = {
    http: 'HTTP 错误', auth: '鉴权失败', quota: '配额不足', rate_limit: '限流', not_found: '不存在',
    no_channel: '无可用渠道', bad_request: '请求错误', server: '服务端错误', client: '客户端错误',
    timeout: '超时', network: '网络错误', canceled: '已取消', stream: '流中断', parse: '解析失败',
    protocol: '协议异常', api: 'API 错误', blocked: '被拦截', empty: '空响应',
  }
  return (t && map[t]) || t || '-'
}

export const truncate = (s?: string, n = 120) => {
  if (!s) return ''
  return s.length > n ? `${s.slice(0, n)}…` : s
}
