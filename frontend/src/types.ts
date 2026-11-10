export type Kind = 'stress' | 'token_audit' | 'request_diff' | 'injection'
export type Protocol = 'auto' | 'openai' | 'claude' | 'gemini'
export type RunStatus = 'queued' | 'running' | 'completed' | 'stopped' | 'failed' | 'interrupted'

export interface Target {
  id: string
  name: string
  baseUrl: string
  protocol: Protocol
  hasKey: boolean
  keyMasked: string
  modelCount?: number
  createdAt: number
  updatedAt: number
  lastTestedAt?: number
}

export interface Prompt {
  id: string
  name: string
  category: string
  content: string
  builtin: boolean
  createdAt: number
  updatedAt: number
}

export interface Check {
  kind: 'canary_leak' | 'contains' | 'regex' | 'refusal'
  value?: string
  description?: string
}

export interface Attack {
  id: string
  name: string
  category: string
  severity: 'low' | 'medium' | 'high' | 'critical'
  systemPrompt: string
  prompt: string
  canary: string
  checks: Check[]
  requireJudge: boolean
  rubric: string
  builtin: boolean
  createdAt: number
  updatedAt: number
}

export interface Price {
  id: string
  pattern: string
  inputPerM: number
  outputPerM: number
  cacheReadPerM: number
  cacheWritePerM: number
  currency: string
  note?: string
  updatedAt: number
}

export interface Stats {
  count: number
  min: number
  max: number
  avg: number
  p50: number
  p90: number
  p95: number
  p99: number
  sum: number
}

export interface TokenStats {
  prompt: number
  completion: number
  total: number
  cached: number
  cacheWrite: number
  reasoning: number
  avgCompletion: number
  avgTokensPerSec: number
  estimated: number
  cost: number
}

export interface StatusCount { status: number; count: number }
export interface ErrorGroup {
  type: string
  message: string
  count: number
  statuses?: number[]
  models?: string[]
  firstAtMs?: number
}
export interface SeriesPoint {
  sec: number
  started: number
  completed: number
  success: number
  failed: number
  activeConcurrency: number
  avgLatencyMs: number
  rps: number
}
export interface HistBucket { label: string; from: number; to: number; count: number }

export interface Aggregate {
  total: number
  success: number
  failed: number
  canceled: number
  successRate: number
  errorRate: number
  qps: number
  elapsedMs: number
  latency: Stats
  ttft: Stats
  tpot: Stats
  tokens: TokenStats
  statusCodes: StatusCount[]
  errors: ErrorGroup[]
  series: SeriesPoint[]
  histogram: HistBucket[]
}

export interface ModelAggregate {
  model: string
  targetId?: string
  targetName?: string
  protocol: string
  endpoint?: string
  aggregate?: Aggregate
  total: number
  success: number
  failed: number
  successRate: number
  errorRate: number
  qps: number
  latency: Stats
  ttft: Stats
  tpot: Stats
  tokens: TokenStats
  statusCodes: StatusCount[]
  errors: ErrorGroup[]
  histogram: HistBucket[]
}

export interface ModelLive {
  model: string
  targetName?: string
  protocol: string
  completed: number
  success: number
  failed: number
  active: number
  avgLatencyMs: number
  ttftMs: number
  qps: number
  successRate: number
}

export interface JudgeVerdict {
  method: string
  verdict: string
  score: number
  reason: string
  category?: string
  model?: string
  tokens?: number
  cost?: number
  raw?: string
}
export interface CheckResult {
  check: Check
  matched: boolean
  evidence?: string
  decisive: boolean
}

export interface Item {
  id: number
  runId: string
  index: number
  attempts: number
  targetId?: string
  targetName?: string
  model: string
  protocol: string
  caseId?: string
  caseName?: string
  category?: string
  severity?: string
  repetition?: number
  startedAt: number
  startOffsetMs: number
  latencyMs: number
  ttftMs: number
  generationMs: number
  tpotMs: number
  status: number
  ok: boolean
  promptTokens: number
  completionTokens: number
  totalTokens: number
  cachedTokens: number
  cacheWriteTokens: number
  reasoningTokens: number
  localPromptTokens?: number
  tokenSource: string
  cost: number
  errorType?: string
  errorMessage?: string
  requestId?: string
  finishReason?: string
  outputPreview?: string
  verdict?: string
  judge?: JudgeVerdict
  checks?: CheckResult[]
  meta?: Record<string, any>
  createdAt?: number
}

export interface ItemDetail extends Item {
  outputText: string
  requestJson: string
  responseJson: string
}

export interface Live {
  runId: string
  kind: Kind
  status: string
  phase?: string
  elapsedMs: number
  planned: number
  completed: number
  success: number
  failed: number
  canceled: number
  successRate: number
  qps: number
  activeConcurrency: number
  currentModel?: string
  latency: Stats
  ttft: Stats
  tpot: Stats
  tokens: TokenStats
  statusCodes: StatusCount[]
  errors: ErrorGroup[]
  series: SeriesPoint[]
  histogram: HistBucket[]
  models: ModelLive[]
  recent: Item[]
  extra?: Record<string, any>
  error?: string
}

export interface RunSummary {
  planned: number
  total: number
  success: number
  failed: number
  canceled: number
  successRate: number
  qps: number
  asr?: number
  elapsedMs: number
  cost?: number
  models?: string[]
  data?: any
}

export interface Run {
  id: string
  kind: Kind
  status: RunStatus
  error?: string
  createdAt: number
  startedAt: number
  finishedAt: number
  durationMs: number
}

export interface RunRow extends Run {
  summary: RunSummary
}

export interface RunView {
  run: Run
  summary: RunSummary
  live?: Live
  config?: any
}

export interface Fingerprints {
  system?: string
  user?: string
  history?: string
  tools?: string
  functionResponse?: string
  generation?: string
  safety?: string
  cached?: string
  media?: string
  webSearch?: string
  other?: string
}

export interface Finding {
  category: string
  rule: string
  severity: 'low' | 'medium' | 'high' | string
  path: string
  snippet: string
  detail?: string
}

export interface Snapshot {
  id: number
  runId: string
  runKind: string
  itemIndex: number
  caseId: string
  caseName: string
  targetId: string
  targetName: string
  targetLabel: string
  model: string
  protocol: string
  endpoint: string
  requestId: string
  repetition: number
  requestTimeMs: number
  bodySha256: string
  bodySize: number
  fingerprints: Fingerprints
  findings: Finding[]
  createdAt: number
}
export interface SnapshotDetail extends Snapshot {
  body: string
  canonical: string
}

export interface DiffChange {
  op: 'add' | 'remove' | 'replace'
  path: string
  category: string
  severity: 'high' | 'medium' | 'low' | string
  before?: any
  after?: any
  truncated?: boolean
}
export interface DiffSection {
  name: string
  label: string
  left: string
  right: string
  changed: boolean
}
export interface DiffSummary {
  total: number
  added: number
  removed: number
  replaced: number
  truncated: boolean
  byCategory: Record<string, number>
  bySeverity: Record<string, number>
}
export interface DiffResult {
  leftSha: string
  rightSha: string
  leftType?: string
  rightType?: string
  changes: DiffChange[]
  summary: DiffSummary
  sections?: DiffSection[]
}
export interface DiffResponse {
  mode: 'wire' | 'canonical'
  left: SnapshotDetail
  right: SnapshotDetail
  result: DiffResult
  warnings: string[]
}

export interface AuditCell {
  targetIndex: number
  targetId: string
  targetLabel: string
  model: string
  protocol: string
  promptId: string
  promptName: string
  count: number
  success: number
  errors: number
  missingUsage: number
  minInput: number
  medianInput: number
  maxInput: number
  avgInput: number
  localTokens: number
  localSource?: string
  overhead: number
  overheadPct: number
  spreadPct: number
  deltaPct: number
  status: string
  reasons?: string[]
  cost: number
  avgLatencyMs: number
}
export interface AuditPromptRow {
  promptId: string
  promptName: string
  baselineInput: number
  cells: AuditCell[]
}
export interface AuditTargetAgg {
  targetIndex: number
  targetId: string
  targetLabel: string
  model: string
  protocol: string
  count: number
  success: number
  errors: number
  medianInput: number
  avgInput: number
  localTokens: number
  overheadPct: number
  anomalies: number
  cost: number
}
export interface AuditAnomaly {
  promptId: string
  promptName: string
  targetLabel: string
  model: string
  status: string
  deltaPct: number
  spreadPct: number
  overheadPct: number
  reasons: string[]
}
export interface AuditReport {
  threshold: number
  baselineMethod: string
  totalRequests: number
  success: number
  errors: number
  missingUsage: number
  localComparable: number
  targets: AuditTargetAgg[]
  prompts: AuditPromptRow[]
  anomalies: AuditAnomaly[]
  notes: string[]
}

export interface RequestDiffCaseInfo {
  id: string
  name: string
  notes?: string
  hasHistory: boolean
  hasTools: boolean
  hasMedia: boolean
  hasWebSearch: boolean
  hasFunctionResponse: boolean
  messageCount: number
}
export interface RequestDiffReport {
  audit?: AuditReport
  targets: { targetId: string; label: string; model: string; protocol: string }[]
  cases: RequestDiffCaseInfo[]
  snapshotCount: number
  findingCount: number
  findingsByCategory: Record<string, number>
  findingsBySeverity: Record<string, number>
  warnings?: string[]
  notes: string[]
}

export interface InjStat {
  total: number
  success: number
  blocked: number
  unclear: number
  errors: number
  asr: number
}
export interface InjTargetAgg extends InjStat {
  targetIndex: number
  targetId: string
  targetLabel: string
  model: string
  protocol: string
}
export interface InjCategoryAgg extends InjStat { category: string }
export interface InjCaseTarget extends InjStat {
  targetIndex: number
  targetLabel: string
  model: string
  lastVerdict: string
  lastReason: string
}
export interface InjCaseRow {
  attackId: string
  attackName: string
  category: string
  severity: string
  targets: InjCaseTarget[]
  overall: InjStat
}
export interface InjectionReport {
  mode: string
  judgeModel?: string
  judgeAvailable: boolean
  overall: InjStat
  targets: InjTargetAgg[]
  categories: InjCategoryAgg[]
  cases: InjCaseRow[]
  judgeTokens: number
  judgeCost: number
  ruleDecisive: number
  llmJudged: number
  notes: string[]
}

export interface Meta {
  version: string
  authEnabled: boolean
  kinds: { kind: Kind; title: string }[]
  protocols: { id: string; label: string; stream: boolean; default: boolean }[]
  categories: { id: string; label: string }[]
  tokenizerStrategies: { id: string; label: string }[]
  limits: { maxConcurrentRuns: number; maxBodyMb: number; retentionDays: number }
  tokenizers: TokenizerStatus
}

export interface TokenizerStatus {
  tokenizerDir: string
  offline: boolean
  tiktoken: boolean
  gemini: {
    name: string
    file: string
    path: string
    downloaded: boolean
    loaded: boolean
    error?: string
  }[]
}

export interface Paged<T> { items: T[]; total: number; limit?: number; offset?: number }
