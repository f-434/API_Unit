import ScrollTable from './ScrollTable'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Alert, Button, Descriptions, Input, Modal, Progress, Space, Spin, Tabs, Tag, Tooltip, message,
} from 'antd'
import { CodeOutlined, EyeOutlined, FileAddOutlined } from '@ant-design/icons'
import { api } from '../api'
import type {
  AuthenticityCheck, AuthenticityLiveModel, AuthenticityModelReport, AuthenticityReport, Live,
} from '../types'
import { EmptyPanel, JsonBlock, Kpi, NotesCollapse } from './common'
import { ItemDrawer } from './RunItems'

const verdictColor: Record<string, string> = {
  authentic: 'green',
  likely: 'cyan',
  suspicious: 'orange',
  counterfeit: 'red',
  inconclusive: 'default',
  error: 'red',
}

const verdictLabel: Record<string, string> = {
  authentic: '通过',
  likely: '较可信',
  suspicious: '可疑',
  counterfeit: '仿冒',
  inconclusive: '证据不足',
  error: '执行错误',
}

const statusColor: Record<string, string> = { pass: 'green', fail: 'red', na: 'default', warn: 'gold' }
const statusLabel: Record<string, string> = { pass: '通过', fail: '失败', na: 'N/A', warn: '警告' }
const weightLabel: Record<string, string> = {
  hard: '硬证据', capability: '能力', metadata: '元数据', soft: '软证据',
}

const groupOrder = ['接口指纹', '响应来源', '工具调用情况', '模型身份', '数据一致性', '思维签名真实性', '自定义探针']

interface ExecRow {
  key: string
  targetId: string
  targetName: string
  model: string
  protocol: string
  source: AuthenticityModelReport['source']
  repetition: number
  totalReps: number
  verdict: string
  confidence: string
  passed: number
  failed: number
  na: number
  durationMs: number
  checks: AuthenticityCheck[]
  error?: string
  modelReport: AuthenticityModelReport
}

function passRate(row: ExecRow): number {
  const denom = row.passed + row.failed
  if (denom <= 0) return 0
  return Math.round((row.passed / denom) * 100)
}

function buildExecRows(report: AuthenticityReport): ExecRow[] {
  const out: ExecRow[] = []
  for (const m of report.models || []) {
    const reps = m.repetitions || 1
    if (m.repVerdicts && m.repVerdicts.length > 0) {
      for (const rv of m.repVerdicts) {
        out.push({
          key: `${m.targetId}:${m.model}:${rv.repetition}`,
          targetId: m.targetId, targetName: m.targetName, model: m.model, protocol: m.protocol,
          source: m.source, repetition: rv.repetition, totalReps: reps,
          verdict: rv.verdict, confidence: rv.confidence,
          passed: rv.passed, failed: rv.failed, na: rv.na, durationMs: rv.durationMs,
          checks: (m.checks || []).filter(c => (c.repetition || 1) === rv.repetition),
          error: rv.error || m.error,
          modelReport: m,
        })
      }
      continue
    }
    if (reps > 1) {
      const grouped = new Map<number, AuthenticityCheck[]>()
      for (const c of m.checks || []) {
        const rep = c.repetition || 1
        const arr = grouped.get(rep) || []
        arr.push(c)
        grouped.set(rep, arr)
      }
      for (const [rep, checks] of grouped) {
        const passed = checks.filter(c => c.status === 'pass' || c.status === 'warn').length
        const failed = checks.filter(c => c.status === 'fail').length
        const na = checks.filter(c => c.status === 'na').length
        out.push({
          key: `${m.targetId}:${m.model}:${rep}`,
          targetId: m.targetId, targetName: m.targetName, model: m.model, protocol: m.protocol,
          source: m.source, repetition: rep, totalReps: reps,
          verdict: m.verdict, confidence: m.confidence,
          passed, failed, na, durationMs: Math.round(m.durationMs / reps),
          checks, error: m.error, modelReport: m,
        })
      }
      continue
    }
    out.push({
      key: `${m.targetId}:${m.model}:1`,
      targetId: m.targetId, targetName: m.targetName, model: m.model, protocol: m.protocol,
      source: m.source, repetition: 1, totalReps: 1,
      verdict: m.verdict, confidence: m.confidence,
      passed: m.passed, failed: m.failed, na: m.na, durationMs: m.durationMs,
      checks: m.checks || [], error: m.error, modelReport: m,
    })
  }
  return out
}

function normalizeGroup(name?: string) {
  const g = (name || '').trim()
  if (g === '外部证明（MCP / 证明页）' || g === '外部证明') return '工具调用情况'
  return g || '其他'
}

function groupChecks(checks: AuthenticityCheck[]) {
  const map = new Map<string, AuthenticityCheck[]>()
  for (const c of checks) {
    const g = normalizeGroup(c.groupName)
    const arr = map.get(g) || []
    arr.push(c)
    map.set(g, arr)
  }
  const ordered: { name: string; checks: AuthenticityCheck[] }[] = []
  for (const g of groupOrder) {
    if (map.has(g)) {
      ordered.push({ name: g, checks: map.get(g)! })
      map.delete(g)
    }
  }
  for (const [name, list] of map) ordered.push({ name, checks: list })
  return ordered
}

function CheckRow({
  check, runId, model, onOpen, onTemplate,
}: {
  check: AuthenticityCheck
  runId: string
  model: string
  onOpen?: (c: AuthenticityCheck) => void
  onTemplate?: (c: AuthenticityCheck) => void
}) {
  const canTemplate = check.id.startsWith('P5-') || (check.caseId || '').startsWith('fp.')
  const [expanded, setExpanded] = useState(false)
  const [loading, setLoading] = useState(false)
  const [detail, setDetail] = useState<{ item?: any; error?: string } | null>(null)

  // 按 caseId 找回产生该检查证据的 item，再取完整正文。列表接口不返回
  // 请求/响应体，所以这里先定位 id，再调用单条详情接口。
  const load = async () => {
    if (!check.caseId) return
    setLoading(true)
    try {
      const res = await api.items(runId, { model, caseId: check.caseId, limit: 50 })
      const hit = (res.items || []).find((it: any) => !check.repetition || it.repetition === check.repetition) || res.items?.[0]
      if (!hit) {
        setDetail({ error: '未找到对应请求正文（可能已被保留策略清理）' })
        return
      }
      const d = await api.item(runId, hit.id)
      setDetail({ item: d.item })
    } catch (e: any) {
      setDetail({ error: e?.message || '读取请求正文失败' })
    } finally {
      setLoading(false)
    }
  }

  const toggle = () => {
    const next = !expanded
    setExpanded(next)
    if (next && !detail && !loading) load()
  }

  return (
    <div className={`auth-check-item is-${check.status}`}>
      <div className={`auth-check-row is-${check.status}`}>
        <Tag color={statusColor[check.status] || 'default'} style={{ marginTop: 1 }}>
          {statusLabel[check.status] || check.status}
        </Tag>
        <div className="auth-check-main">
          <div className="auth-check-name">{check.name}</div>
          <div className="auth-check-meta">
            <span>ID {check.id}</span>
            <span>证据 {weightLabel[check.weight] || check.weight}</span>
            {!!check.repetition && <span>第 {check.repetition} 次</span>}
            {!!check.durationMs && <span>{check.durationMs} ms</span>}
          </div>
          {check.evidence && <div className="auth-check-evidence">{check.evidence}</div>}
        </div>
        <div className="auth-check-actions">
          {check.caseId && (
            <Button size="small" type="link" icon={<CodeOutlined />} onClick={toggle}>
              {expanded ? '收起' : '请求/响应'}
            </Button>
          )}
          {canTemplate && onTemplate && (
            <Tooltip title="用这条请求/响应生成模板草稿；审核并发布后才会生效">
              <Button size="small" type="link" icon={<FileAddOutlined />} onClick={() => onTemplate(check)}>用例</Button>
            </Tooltip>
          )}
        </div>
      </div>
      {expanded && (
        <div className="auth-check-expanded">
          {loading ? (
            <div style={{ padding: 8 }}><Spin size="small" /> <span className="muted">读取请求/响应…</span></div>
          ) : detail?.error ? (
            <Alert type="warning" showIcon message={detail.error} />
          ) : detail?.item ? (
            <>
              <Descriptions size="small" column={3} style={{ marginBottom: 8 }}>
                <Descriptions.Item label="用例">{detail.item.caseName || check.caseId}</Descriptions.Item>
                <Descriptions.Item label="HTTP / 状态">{detail.item.status || '-'}</Descriptions.Item>
                <Descriptions.Item label="请求 ID"><span className="small-mono">{detail.item.requestId || '-'}</span></Descriptions.Item>
              </Descriptions>
              <Tabs
                size="small"
                items={[
                  {
                    key: 'req', label: '请求体',
                    children: detail.item.requestJson
                      ? <JsonBlock value={detail.item.requestJson} maxHeight={240} />
                      : <Alert type="info" showIcon message="该次请求没有保存请求体" />,
                  },
                  {
                    key: 'resp', label: '响应体',
                    children: detail.item.responseJson
                      ? <JsonBlock value={detail.item.responseJson} maxHeight={300} />
                      : <Alert type="info" showIcon message="该次请求没有保存响应体" />,
                  },
                  {
                    key: 'headers', label: '响应头',
                    children: Object.keys(detail.item.meta?.responseHeaders || {}).length
                      ? <JsonBlock value={detail.item.meta.responseHeaders} maxHeight={240} />
                      : <Alert type="info" showIcon message="该次请求没有保存响应头（旧数据或请求失败）" />,
                  },
                ]}
              />
              {onOpen && (
                <Button size="small" type="link" icon={<EyeOutlined />} onClick={() => onOpen(check)}>打开完整详情</Button>
              )}
            </>
          ) : (
            <p className="muted" style={{ margin: 0 }}>暂无关联的请求/响应</p>
          )}
        </div>
      )}
    </div>
  )
}

function buildTemplateSpec(item: any): string {
  const spec: any = {}
  if (item?.status) spec.status = item.status
  try {
    const body = JSON.parse(item?.responseJson || '{}')
    const err = body?.error
    if (err && typeof err === 'object') {
      const paths: any[] = []
      for (const key of ['code', 'status', 'type', 'param']) {
        if (err[key] !== undefined && err[key] !== '') {
          paths.push({ path: `error.${key}`, equals: String(err[key]) })
        }
      }
      if (paths.length) spec.jsonPaths = paths
      if (typeof err.message === 'string' && err.message.length > 0 && err.message.length <= 300) {
        spec.contains = [err.message]
      }
    }
  } catch {
    // Status-only template is still useful.
  }
  return JSON.stringify(spec, null, 2)
}

export default function AuthenticityReportView({
  report, runId, live,
}: {
  report?: AuthenticityReport
  runId: string
  live?: Live
}) {
  const [detail, setDetail] = useState<{ itemId?: number; open: boolean }>({ open: false })
  const [expandedKeys, setExpandedKeys] = useState<string[]>([])
  const [tplModal, setTplModal] = useState<any>({ open: false })
  const [tplSaving, setTplSaving] = useState(false)
  const navigate = useNavigate()

  const rows = useMemo(() => report ? buildExecRows(report) : [], [report])

  useEffect(() => {
    setExpandedKeys([])
  }, [report])

  const openCheck = async (model: AuthenticityModelReport, check: AuthenticityCheck) => {
    if (!check.caseId) {
      message.info('该检查没有关联的请求正文')
      return
    }
    try {
      const res = await api.items(runId, { model: model.model, caseId: check.caseId, limit: 50 })
      const hit = (res.items || []).find((it: any) => !check.repetition || it.repetition === check.repetition) || res.items?.[0]
      if (!hit) {
        message.info('未找到对应请求正文（可能已被保留策略清理）')
        return
      }
      setDetail({ itemId: hit.id, open: true })
    } catch (e: any) {
      message.error(e?.message || '读取请求正文失败')
    }
  }

  const openTemplateModal = async (model: AuthenticityModelReport, check: AuthenticityCheck) => {
    const probeId = check.id.startsWith('P5-') ? `usage.${model.protocol}` : (check.caseId || '')
    if (!probeId || (!probeId.startsWith('fp.') && !probeId.startsWith('usage.'))) {
      message.info('该探针暂不支持模板覆盖；目前支持指纹（fp.*）和 usage 规则（usage.*）')
      return
    }
    let item: any = null
    if (check.caseId) {
      try {
        const res = await api.items(runId, { model: model.model, caseId: check.caseId, limit: 50 })
        const hit = (res.items || []).find((it: any) => !check.repetition || it.repetition === check.repetition) || res.items?.[0]
        if (hit) {
          const d = await api.item(runId, hit.id)
          item = d.item
        }
      } catch {
        // Allow creating a spec-only draft.
      }
    }
    let spec = '{}'
    if (probeId.startsWith('usage.')) {
      spec = JSON.stringify({
        usage: {
          includeReasoningInTotal: model.protocol === 'gemini',
          allowReasoningGtCompletion: model.protocol === 'gemini',
        },
      }, null, 2)
    } else if (item) {
      spec = buildTemplateSpec(item)
    }
    setTplModal({
      open: true, probeId, provider: model.protocol,
      name: check.name, note: '', spec,
      sourceRunId: runId, sourceItemId: item?.id,
      sampleRequest: item?.requestJson, sampleResponse: item?.responseJson,
    })
  }

  const saveTemplate = async () => {
    try {
      JSON.parse(tplModal.spec || '{}')
    } catch (e: any) {
      message.error('Spec 不是合法 JSON: ' + (e?.message || ''))
      return
    }
    setTplSaving(true)
    try {
      await api.createAuthenticityTemplate({
        probeId: tplModal.probeId,
        provider: tplModal.provider,
        name: tplModal.name,
        note: tplModal.note,
        spec: tplModal.spec,
        sourceRunId: tplModal.sourceRunId,
        sourceItemId: tplModal.sourceItemId,
        createdBy: 'ui',
      })
      message.success(<span>已保存为草稿（不会立即生效）。<a onClick={() => navigate('/authenticity/templates')}>去审核发布</a></span>)
      setTplModal({ open: false })
    } catch (e: any) {
      message.error(e?.message || '保存失败')
    } finally {
      setTplSaving(false)
    }
  }

  if (!report) {
    const done = Number(live?.extra?.authenticityDone || 0)
    const total = Number(live?.extra?.authenticityTotal || 0)
    const models = (live?.extra?.authenticityLive || []) as AuthenticityLiveModel[]
    const running = models.filter(m => !m.done).length
    return (
      <div>
        <div className="kpi-grid" style={{ marginBottom: 14 }}>
          <Kpi label="已完成轮次 / 总轮次" value={`${done} / ${total || models.length}`} />
          <Kpi label="进行中" value={running} color={running ? '#0891b2' : undefined} />
          <Kpi label="耗时" value={live ? `${(live.elapsedMs / 1000).toFixed(0)}s` : '-'} />
          <Kpi label="当前模型" value={live?.currentModel || '-'} />
        </div>
        {models.length === 0 ? <EmptyPanel text="真实性检测运行中，等待第一批模型数据…" /> : (
          <ScrollTable
            rowKey={m => `${m.targetId}:${m.model}:${m.repetition}`}
            size="small"
            dataSource={models}
            pagination={false}
            scroll={{ x: 1260 }}
            expandable={{
              expandedRowRender: m => (
                <div style={{ padding: '4px 0 10px' }}>
                  {m.error && <Alert type="error" showIcon style={{ marginBottom: 10 }} message="当前错误" description={m.error} />}
                  {groupChecks(m.checks || []).map(g => (
                    <div className="auth-check-group" key={g.name}>
                      <div className="auth-check-group-title">{g.name}<span className="count">{g.checks.length} 项</span></div>
                      {g.checks.map((c, i) => (
                        <CheckRow key={c.id + c.name + i} check={c} runId={runId} model={m.model} />
                      ))}
                    </div>
                  ))}
                </div>
              ),
            }}
            columns={[
              { title: '上游', dataIndex: 'targetName', width: 150 },
              { title: '模型', dataIndex: 'model', width: 210, render: (v: string) => <span className="mono">{v}</span> },
              {
                title: '轮次', width: 86,
                render: (_: any, m: AuthenticityLiveModel) => <Tag>第 {m.repetition || 1}/{m.totalRepetitions || 1} 次</Tag>,
              },
              {
                title: '当前环节', dataIndex: 'stage', width: 230,
                render: (v: string, m: AuthenticityLiveModel) => (
                  <Space size={4}>
                    {!m.done && <Tag color="processing">进行中</Tag>}
                    <span>{v || '等待调度'}</span>
                  </Space>
                ),
              },
              {
                title: '通过 / 失败 / N/A', width: 180,
                render: (_: any, m: AuthenticityLiveModel) => {
                  const cs = m.checks || []
                  return <span>
                    <Tag color="green">{cs.filter(c => c.status === 'pass').length}</Tag>
                    {cs.some(c => c.status === 'fail') && <Tag color="red">失败 {cs.filter(c => c.status === 'fail').length}</Tag>}
                    {cs.some(c => c.status === 'na') && <Tag>N/A {cs.filter(c => c.status === 'na').length}</Tag>}
                  </span>
                },
              },
              {
                title: '状态', width: 120,
                render: (_: any, m: AuthenticityLiveModel) => m.done
                  ? <Tag color={verdictColor[m.verdict || ''] || 'default'}>{verdictLabel[m.verdict || ''] || m.verdict || '完成'}</Tag>
                  : <Tag color="processing">运行中</Tag>,
              },
              { title: '耗时', dataIndex: 'elapsedMs', width: 90, render: (v: number) => <span>{((v || 0) / 1000).toFixed(1)}s</span> },
              {
                title: '最近失败', width: 300, ellipsis: { showTitle: false },
                render: (_: any, m: AuthenticityLiveModel) => {
                  const failed = (m.checks || []).filter(c => c.status === 'fail')
                  const text = failed.length ? `${failed[failed.length - 1].name}: ${failed[failed.length - 1].evidence || ''}` : ''
                  return <Tooltip title={text || '-'}><span className="small-mono" style={{ color: text ? '#dc2626' : undefined }}>{text || '-'}</span></Tooltip>
                },
              },
            ]}
          />
        )}
      </div>
    )
  }

  return (
    <div>
      <Space style={{ marginBottom: 10 }}>
        <Button size="small" onClick={() => setExpandedKeys(rows.map(r => r.key))}>展开全部</Button>
        <Button size="small" onClick={() => setExpandedKeys([])}>收起全部</Button>
      </Space>

      <ScrollTable
        rowKey="key"
        size="small"
        dataSource={rows}
        pagination={false}
        scroll={{ x: 1280 }}
        expandable={{
          expandedRowKeys: expandedKeys,
          onExpandedRowsChange: keys => setExpandedKeys((keys as any[]).map(String)),
          expandedRowRender: row => (
            <div style={{ padding: '6px 0 12px' }}>
              {row.error && <Alert type="error" showIcon style={{ marginBottom: 10 }} message="执行错误" description={row.error} />}
              <Descriptions size="small" column={3} style={{ marginBottom: 8 }}>
                <Descriptions.Item label="来源">
                  <Space size={4} wrap>
                    <span>{row.source?.name || '-'}</span>
                    <span className="muted">{row.source?.confidence || '-'}</span>
                    {row.source?.gateway && <Tag color="gold">经中转</Tag>}
                  </Space>
                  {!!row.source?.evidence?.length && (
                    <div className="small-mono muted" style={{ marginTop: 2 }}>{row.source.evidence.join('；')}</div>
                  )}
                  {row.source?.gateway && (
                    <div className="small-mono muted" style={{ marginTop: 2 }}>网关特征：{row.source.gateway}</div>
                  )}
                </Descriptions.Item>
                <Descriptions.Item label="端点" span={2}><span className="small-mono">{row.modelReport.endpoint || '-'}</span></Descriptions.Item>
              </Descriptions>
              {groupChecks(row.checks).map(g => (
                <div className="auth-check-group" key={g.name}>
                  <div className="auth-check-group-title">{g.name}<span className="count">{g.checks.length} 项</span></div>
                  {g.checks.map((c, i) => (
                    <CheckRow
                      key={c.id + c.name + (c.repetition || 0) + i}
                      check={c}
                      runId={runId}
                      model={row.model}
                      onOpen={cc => openCheck(row.modelReport, cc)}
                      onTemplate={cc => openTemplateModal(row.modelReport, cc)}
                    />
                  ))}
                </div>
              ))}
              {row.checks.length === 0 && <EmptyPanel text="没有检查项" />}
            </div>
          ),
        }}
        columns={[
          { title: '上游', dataIndex: 'targetName', width: 150 },
          { title: '模型', dataIndex: 'model', width: 200, render: (v: string) => <span className="mono">{v}</span> },
          {
            title: '轮次', width: 86,
            render: (_: any, r: ExecRow) => <Tag>第 {r.repetition}/{r.totalReps} 次</Tag>,
          },
          {
            title: '来源', width: 230,
            render: (_: any, r: ExecRow) => {
              const detail = [
                ...(r.source?.evidence || []),
                r.source?.gateway ? `网关特征：${r.source.gateway}` : '',
              ].filter(Boolean).join('；')
              return (
                <Tooltip title={detail || '未识别到可用的来源特征'}>
                  <Space size={4} wrap>
                    <Tag color={r.source?.confidence === 'high' ? 'green' : r.source?.confidence === 'medium' ? 'blue' : 'gold'}>
                      {r.source?.name || '未识别'}
                    </Tag>
                    <span className="muted" style={{ fontSize: 12 }}>{r.source?.confidence || 'unknown'}</span>
                    {r.source?.gateway && <Tag color="gold">经中转</Tag>}
                  </Space>
                </Tooltip>
              )
            },
          },
          {
            title: '结论', dataIndex: 'verdict', width: 100,
            render: (v: string) => <Tag color={verdictColor[v] || 'default'}>{verdictLabel[v] || v}</Tag>,
          },
          {
            title: '通过率', width: 170,
            render: (_: any, r: ExecRow) => (
              <Progress
                percent={passRate(r)}
                size="small"
                status={passRate(r) >= 100 ? 'success' : passRate(r) < 60 ? 'exception' : 'normal'}
              />
            ),
          },
          {
            title: '通过 / 失败 / N/A', width: 160,
            render: (_: any, r: ExecRow) => (
              <span><b style={{ color: '#16a34a' }}>{r.passed}</b> / <b style={{ color: r.failed ? '#dc2626' : undefined }}>{r.failed}</b> / {r.na}</span>
            ),
          },
          { title: '耗时', dataIndex: 'durationMs', width: 90, render: (v: number) => <span>{((v || 0) / 1000).toFixed(1)}s</span> },
          {
            title: '失败原因', width: 320, ellipsis: { showTitle: false },
            render: (_: any, r: ExecRow) => {
              const failed = (r.checks || []).filter(c => c.status === 'fail')
              const last = failed[failed.length - 1]
              const text = r.error || (last ? `${last.name}: ${last.evidence || ''}` : '')
              return (
                <Tooltip title={<div style={{ whiteSpace: 'pre-wrap', maxWidth: 720 }}>{text || '-'}</div>}>
                  <span className="small-mono" style={{ color: text ? '#dc2626' : undefined }}>{text || '-'}</span>
                </Tooltip>
              )
            },
          },
        ]}
      />

      <NotesCollapse notes={report.notes} />

      <Modal
        open={!!tplModal.open}
        onCancel={() => setTplModal({ open: false })}
        onOk={saveTemplate}
        okText="保存为草稿"
        cancelText="取消"
        confirmLoading={tplSaving}
        width={900}
        title="用这条请求/响应生成用例模板"
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="只创建草稿，不会立即生效"
          description="保存后需到「真实性用例」页审核并发布；发布会归档同一 probe/provider 的旧 active 版本。"
        />
        <Descriptions size="small" column={2} bordered style={{ marginBottom: 12 }}>
          <Descriptions.Item label="Probe ID"><span className="small-mono">{tplModal.probeId}</span></Descriptions.Item>
          <Descriptions.Item label="Provider">{tplModal.provider || 'all'}</Descriptions.Item>
          <Descriptions.Item label="名称" span={2}>
            <Input value={tplModal.name} onChange={e => setTplModal((m: any) => ({ ...m, name: e.target.value }))} />
          </Descriptions.Item>
          <Descriptions.Item label="说明" span={2}>
            <Input value={tplModal.note} onChange={e => setTplModal((m: any) => ({ ...m, note: e.target.value }))} placeholder="例如：官方 Gemini 经 New API 改写后的错误信封" />
          </Descriptions.Item>
        </Descriptions>
        <h3>期望 Spec</h3>
        <Input.TextArea
          className="code-input"
          autoSize={{ minRows: 6, maxRows: 16 }}
          value={tplModal.spec}
          onChange={e => setTplModal((m: any) => ({ ...m, spec: e.target.value }))}
        />
        <h3>样本请求</h3>
        {tplModal.sampleRequest ? <JsonBlock value={tplModal.sampleRequest} maxHeight={220} /> : <p className="muted">没有关联请求正文</p>}
        <h3>样本响应</h3>
        {tplModal.sampleResponse ? <JsonBlock value={tplModal.sampleResponse} maxHeight={260} /> : <p className="muted">没有关联响应正文</p>}
      </Modal>

      <ItemDrawer
        runId={runId}
        itemId={detail.itemId}
        open={detail.open}
        onClose={() => setDetail({ open: false })}
      />
    </div>
  )
}
