export type Kind = 'stress' | 'token_audit' | 'request_diff' | 'injection' | 'authenticity' | 'image_detection'
export type Protocol = 'auto' | 'openai' | 'claude' | 'gemini'
export type RunStatus = 'queued' | 'running' | 'completed' | 'stopped' | 'failed' | 'interrupted'

export interface TargetRef {
  id: string
  name: string
}

export interface Target {
  id: string
  name: string
  baseUrl: string
  /** 自定义模型列表接口；为空时自动探测。 */
  modelsPath?: string
  /** Token 口径路径，例如 gemini/vt；用于匹配手工 Token 基准。 */
  tokenFamily?: string
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
  /** 按口径路径维护的本地 Token 基准，例如 { "gemini/vt": 34 }。 */
  tokenOverrides?: Record<string, number>
  tokenOverridesHash?: string
  /** 内容已变更，当前基准不参与审计比较。 */
  tokenOverridesStale?: boolean
  createdAt: number
  updatedAt: number
}

export interface TokenFamilyNode {
  key: string
  label: string
  children?: TokenFamilyNode[]
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
  rpm: number
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
  responseModels?: string[]
  aggregate?: Aggregate
  total: number
  success: number
  failed: number
  successRate: number
  errorRate: number
  qps: number
  rpm: number
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
  responseModels?: string[]
  completed: number
  success: number
  failed: number
  active: number
  avgLatencyMs: number
  ttftMs: number
  qps: number
  rpm: number
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
  /** Model the upstream actually reported in its response. */
  responseModel?: string
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
  rpm: number
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
  rpm: number
  asr?: number
  elapsedMs: number
  cost?: number
  models?: string[]
  targets?: TargetRef[]
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
  tokenFamily?: string
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
  tokenFamily?: string
  responseModels?: string[]
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
  responseModels?: string[]
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

export type AuthenticityVerdict =
  | 'authentic' | 'likely' | 'suspicious' | 'counterfeit' | 'inconclusive' | 'error'

export interface AuthenticitySource {
  name: string
  confidence: 'high' | 'medium' | 'low' | 'unknown' | string
  evidence?: string[]
  /** 中转网关特征（如 X-Oneapi-Request-Id），与上游来源分开展示 */
  gateway?: string
}

export interface AuthenticityCheck {
  id: string
  group: string
  groupName: string
  name: string
  status: 'pass' | 'fail' | 'na' | 'warn'
  weight: 'hard' | 'capability' | 'metadata' | 'soft' | string
  evidence?: string
  durationMs?: number
  caseId?: string
  repetition?: number
}

export interface AuthenticityDimension {
  key: string
  name: string
  passed: number
  failed: number
  na: number
}

export interface AuthenticityChallenge {
  toolName: string
  nonce: string
  secret: string
  a: number
  b: number
  sum: number
}

export interface AuthenticityRepVerdict {
  repetition: number
  verdict: AuthenticityVerdict
  confidence: string
  passed: number
  failed: number
  na: number
  durationMs: number
  error?: string
}

export interface AuthenticityModelReport {
  targetId: string
  targetName: string
  model: string
  protocol: string
  endpoint: string
  responseModel?: string
  source: AuthenticitySource
  verdict: AuthenticityVerdict
  verdictLabel: string
  confidence: string
  repetition?: number
  repetitions: number
  repVerdicts?: AuthenticityRepVerdict[]
  passed: number
  failed: number
  na: number
  total: number
  durationMs: number
  dimensions: AuthenticityDimension[]
  checks: AuthenticityCheck[]
  challenge?: AuthenticityChallenge
  error?: string
}

export interface AuthenticityLiveModel {
  targetId: string
  targetName: string
  model: string
  protocol: string
  repetition: number
  totalRepetitions: number
  stage: string
  done: boolean
  verdict?: AuthenticityVerdict | ''
  error?: string
  startedAt: number
  elapsedMs: number
  checks: AuthenticityCheck[]
}

export interface AuthenticityReport {
  models: AuthenticityModelReport[]
  summary: {
    models: number
    authentic: number
    likely: number
    suspicious: number
    counterfeit: number
    inconclusive: number
    error: number
    passed: number
    failed: number
    na: number
  }
  notes: string[]
}

export interface AuthenticityTemplate {
  id: string
  probeId: string
  provider: string
  name: string
  version: number
  status: 'draft' | 'active' | 'archived' | string
  spec: string
  sampleRequest?: string
  sampleResponse?: string
  note?: string
  sourceRunId?: string
  sourceItemId?: number
  createdBy?: string
  createdAt: number
}

export interface ImageDetectionCheck {
  name: string
  status: 'pass' | 'fail' | 'na' | 'warn' | string
  detail?: string
}

export interface ImageDetectionLocalSignature {
  status: 'detected' | 'not_detected' | 'possible' | 'unknown' | string
  evidence?: string[]
}

export interface ImageDetectionOnlineC2PA {
  outcome?: 'detected' | 'not_detected' | string
  validationState?: 'trusted' | 'valid' | 'invalid' | 'not_present' | string
  issuer?: string
  model?: string
  generatedAt?: string
}

export interface ImageDetectionOnlineSynthID {
  outcome?: 'detected' | 'not_detected' | string
  model?: string
  generatedAt?: string
}

export interface ImageDetectionOnlineSignature {
  status: 'checked' | 'unconfigured' | 'error' | 'skipped' | string
  error?: string
  c2pa: ImageDetectionOnlineC2PA
  synthId: ImageDetectionOnlineSynthID
  createdAt?: number
  raw?: string
}

export interface ImageDetectionVerifyLink {
  key: 'adobe' | 'openai' | 'azure' | string
  label: string
  url: string
  mode?: 'auto' | 'upload' | string
  hint?: string
  autoUrl?: string
}

export interface ImageDetectionSignature {
  c2pa: ImageDetectionLocalSignature
  synthId: ImageDetectionLocalSignature
  online?: ImageDetectionOnlineSignature
  links: ImageDetectionVerifyLink[]
  notes?: string[]
}

export interface ImageDetectionImage {
  path: string
  upstreamUrl?: string
  format: string
  width: number
  height: number
  bytes: number
  sha256: string
  signature: ImageDetectionSignature
}

export interface ImageDetectionPhase {
  id: string
  name: string
  status: 'pass' | 'fail' | 'na' | 'warn' | string
  reason?: string
  statusCode?: number
  responseModel?: string
  checks?: ImageDetectionCheck[]
  image?: ImageDetectionImage
  durationMs: number
  requestId?: string
  error?: string
  requestJson?: string
  responseJson?: string
}

export interface ImageDetectionModelReport {
  targetId: string
  targetName: string
  model: string
  kind: string
  size: string
  protocol: string
  endpoint?: string
  passed: number
  failed: number
  na: number
  total: number
  durationMs: number
  phases: ImageDetectionPhase[]
  error?: string
}

export interface ImageDetectionSummary {
  models: number
  phases: number
  passed: number
  failed: number
  na: number
  c2paDetected: number
  synthIdDetected: number
  onlineChecked: number
}

export interface ImageDetectionReport {
  models: ImageDetectionModelReport[]
  summary: ImageDetectionSummary
  notes: string[]
}

export interface ImageDetectionLiveModel {
  targetId: string
  targetName: string
  model: string
  kind: string
  size: string
  stage: string
  done: boolean
  passed: number
  failed: number
  na: number
  error?: string
  startedAt: number
  elapsedMs: number
}

export interface ImageDetectionSettings {
  baseUrl: string
  hasKey: boolean
  keyMasked: string
  endpoint: string
  /** 是否配置了生图请求代理。 */
  proxyConfigured?: boolean
  /** 代理地址掩码（密码已隐藏）。 */
  proxyMasked?: string
  /** 是否强制生图请求使用 TLS 1.2。 */
  forceTls12?: boolean
}

export interface ImageDetectionConfigResponse {
  settings: ImageDetectionSettings
  phases: { name: string; label: string; kind: string }[]
}

export interface AuthenticityHostedConfig {
  profile: 'generic' | 't1y-legacy' | 'custom' | string
  defaultEnabled: boolean
  providers: Record<string, boolean>
  mcp: {
    serverLabel: string
    serverDescription: string
    serverUrl: string
    headers: Record<string, string>
    requireApproval: string
    toolName: string
  }
  proof: {
    baseUrl: string
    domain: string
    pathTemplate: string
    expectedNonceField: string
  }
  legacy: {
    domain: string
    envVar: string
    expectedEnvValue: string
    collection: string
    expectedRecord: Record<string, any>
    keyword: string
  }
}

export interface Meta {
  version: string
  authEnabled: boolean
  kinds: { kind: Kind; title: string }[]
  protocols: { id: string; label: string; stream: boolean; default: boolean }[]
  categories: { id: string; label: string }[]
  tokenizerStrategies: { id: string; label: string }[]
  tokenFamilies: TokenFamilyNode[]
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
