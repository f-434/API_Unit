import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Alert, Button, Col, Empty, Input, Row, Select, Segmented, Space, Table, Tag, Tooltip, Typography, message,
} from 'antd'
import { DiffOutlined, DownloadOutlined, EyeOutlined, HistoryOutlined, SearchOutlined } from '@ant-design/icons'
import { api, download } from '../api'
import type { DiffChange, DiffResponse, RequestDiffReport, Snapshot } from '../types'
import { categoryLabel, fmtBytes, fmtTime, opColor, opLabel, severityColor, severityLabel, truncate } from '../format'
import { CopyButton, EmptyPanel, JsonBlock, Kpi, SeverityTag } from './common'
import { BarChart } from './charts'
import JsonTree, { type HighlightMap } from './JsonTree'
import SnapshotDrawer from './SnapshotDrawer'

function snapshotLabel(s: Snapshot) {
  return `#${s.id} · ${s.targetLabel} · ${s.model} · ${s.caseName} · rep${s.repetition}`
}

export function SnapshotTable({ runId, report }: { runId: string; report?: RequestDiffReport }) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [caseId, setCaseId] = useState<string>()
  const [model, setModel] = useState<string>()
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [snapshotId, setSnapshotId] = useState<number>()

  const { data, isLoading } = useQuery({
    queryKey: ['runSnapshots', runId, page, pageSize, caseId, model, q],
    queryFn: () => api.runSnapshots(runId, { limit: pageSize, offset: (page - 1) * pageSize, caseId, model, q }),
  })
  const models = report?.targets?.map(t => t.model) || []

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select allowClear placeholder="全部用例" value={caseId} onChange={v => { setCaseId(v); setPage(1) }} style={{ width: 200 }}
          options={(report?.cases || []).map(c => ({ value: c.id, label: c.name }))} />
        <Select allowClear placeholder="全部模型" value={model} onChange={v => { setModel(v); setPage(1) }} style={{ width: 220 }}
          options={Array.from(new Set(models)).map(m => ({ value: m, label: m }))} showSearch />
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索用例/标签/请求 ID/SHA" value={q} onChange={e => { setQ(e.target.value); setPage(1) }} style={{ width: 280 }} />
        <Tag>{data?.total ?? 0} 份快照</Tag>
      </Space>
      <Table
        rowKey="id"
        size="small"
        loading={isLoading}
        dataSource={data?.items || []}
        scroll={{ x: 1200 }}
        pagination={{ current: page, pageSize, total: data?.total || 0, showSizeChanger: true, onChange: (p, ps) => { setPage(p); setPageSize(ps) } }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 70 },
          { title: '请求时间', dataIndex: 'requestTimeMs', width: 160, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
          { title: '用例', dataIndex: 'caseName', width: 170 },
          { title: '目标', dataIndex: 'targetLabel', width: 120 },
          { title: '模型', dataIndex: 'model', width: 170, render: (v: string) => <span className="mono">{v}</span> },
          { title: '重复', dataIndex: 'repetition', width: 70 },
          { title: '请求 ID', dataIndex: 'requestId', width: 170, render: (v: string) => <span className="small-mono">{truncate(v, 28)}</span> },
          {
            title: '正文 SHA-256', dataIndex: 'bodySha256', width: 180,
            render: (v: string) => <Tooltip title={v}><span className="small-mono">{v?.slice(0, 16)}…</span></Tooltip>,
          },
          { title: '大小', dataIndex: 'bodySize', width: 90, render: (v: number) => fmtBytes(v) },
          {
            title: '线索', dataIndex: 'findings', width: 80,
            render: (v: any[]) => v?.length ? <Tag color="red">{v.length}</Tag> : <Tag color="green">0</Tag>,
          },
          { title: '', width: 50, render: (_: any, r: Snapshot) => <Button size="small" type="text" icon={<EyeOutlined />} onClick={() => { setSnapshotId(r.id); setOpen(true) }} /> },
        ]}
        onRow={r => ({ onDoubleClick: () => { setSnapshotId(r.id); setOpen(true) }, style: { cursor: 'pointer' } })}
      />
      <SnapshotDrawer id={snapshotId} open={open} onClose={() => setOpen(false)} />
    </div>
  )
}

export function FindingsTable({ runId }: { runId: string }) {
  const [severity, setSeverity] = useState<string>()
  const [category, setCategory] = useState<string>()
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [snapshotId, setSnapshotId] = useState<number>()
  const { data, isLoading } = useQuery({
    queryKey: ['snapshotsForFindings', runId],
    queryFn: () => api.runSnapshots(runId, { limit: 500 }),
  })
  const rows = useMemo(() => {
    const out: any[] = []
    for (const s of data?.items || []) {
      for (const f of s.findings || []) {
        if (severity && f.severity !== severity) continue
        if (category && f.category !== category) continue
        if (q && !(`${s.caseName} ${s.targetLabel} ${s.model} ${f.rule} ${f.snippet} ${f.path}`.toLowerCase().includes(q.toLowerCase()))) continue
        out.push({ ...f, snapshot: s })
      }
    }
    const order: Record<string, number> = { high: 0, medium: 1, low: 2 }
    return out.sort((a, b) => (order[a.severity] ?? 9) - (order[b.severity] ?? 9))
  }, [data, severity, category, q])

  const categories = Array.from(new Set((data?.items || []).flatMap(s => s.findings.map(f => f.category))))

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="隐藏指令扫描"
        description="扫描 System Prompt、User Prompt、历史对话、Tools 描述、Function Response、多模态 URL/文本与其他额外字段，识别忽略指令、角色覆盖、越权泄露、零宽字符、双向控制符、HTML 注释、base64 长文本等线索。仅展示最近 500 份快照。"
      />
      <Space wrap style={{ marginBottom: 12 }}>
        <Select allowClear placeholder="级别" value={severity} onChange={setSeverity} style={{ width: 120 }} options={['high', 'medium', 'low'].map(v => ({ value: v, label: severityLabel(v) }))} />
        <Select allowClear placeholder="分类" value={category} onChange={setCategory} style={{ width: 170 }} options={categories.map(c => ({ value: c, label: categoryLabel(c) }))} />
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索规则/证据/用例" value={q} onChange={e => setQ(e.target.value)} style={{ width: 260 }} />
        <Tag color="red">线索 {rows.length}</Tag>
      </Space>
      <Table
        rowKey={(r, i) => `${r.snapshot.id}-${i}`}
        size="small"
        loading={isLoading}
        dataSource={rows}
        scroll={{ x: 1300 }}
        pagination={{ pageSize: 30 }}
        columns={[
          { title: '级别', dataIndex: 'severity', width: 80, render: (v: string) => <SeverityTag severity={v} /> },
          { title: '分类', dataIndex: 'category', width: 140, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
          { title: '规则', dataIndex: 'rule', width: 160 },
          { title: 'JSONPath', dataIndex: 'path', width: 230, render: (v: string) => <span className="small-mono">{v}</span> },
          { title: '证据', dataIndex: 'snippet', render: (v: string) => <span className="small-mono">{truncate(v, 180)}</span> },
          { title: '用例', width: 150, render: (_: any, r) => r.snapshot.caseName },
          { title: '目标 / 模型', width: 200, render: (_: any, r) => <span>{r.snapshot.targetLabel}<div className="muted small-mono">{r.snapshot.model}</div></span> },
          { title: '', width: 50, render: (_: any, r) => <Button size="small" type="text" icon={<EyeOutlined />} onClick={() => { setSnapshotId(r.snapshot.id); setOpen(true) }} /> },
        ]}
      />
      <SnapshotDrawer id={snapshotId} open={open} onClose={() => setOpen(false)} />
    </div>
  )
}

function parseMaybe(text: string) {
  try { return JSON.parse(text || '{}') } catch { return null }
}

function changeSummary(changes: DiffChange[], op: string) {
  return changes.filter(c => c.op === op)
}

export function DiffPanel({ runId }: { runId: string }) {
  const [localSnapshots, setLocalSnapshots] = useState<Snapshot[]>([])
  const [historySnapshots, setHistorySnapshots] = useState<Snapshot[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [leftId, setLeftId] = useState<number>()
  const [rightId, setRightId] = useState<number>()
  const [mode, setMode] = useState<'wire' | 'canonical'>('wire')
  const [result, setResult] = useState<DiffResponse>()
  const [activePath, setActivePath] = useState<string>()

  const { data: local } = useQuery({
    queryKey: ['diffSnapshots', runId],
    queryFn: () => api.runSnapshots(runId, { limit: 500 }),
  })

  useEffect(() => {
    if (local?.items) {
      setLocalSnapshots(local.items)
      const asc = [...local.items].sort((a, b) => a.id - b.id)
      if (leftId === undefined && asc.length > 0) setLeftId(asc[0].id)
      if (rightId === undefined && asc.length > 1) setRightId(asc[asc.length - 1].id)
    }
  }, [local, leftId, rightId])

  const all = useMemo(() => {
    const map = new Map<number, Snapshot>()
    for (const s of localSnapshots) map.set(s.id, s)
    for (const s of historySnapshots) map.set(s.id, s)
    return Array.from(map.values()).sort((a, b) => a.id - b.id)
  }, [localSnapshots, historySnapshots])
  const options = all.map(s => ({ value: s.id, label: snapshotLabel(s) }))

  const loadHistory = async () => {
    setHistoryLoading(true)
    try {
      const r = await api.snapshots({ limit: 300, order: 'desc' })
      setHistorySnapshots(r.items)
      message.success(`已加载 ${r.items.length} 份历史快照`)
    } catch (e: any) {
      message.error(e?.message || '加载失败')
    } finally {
      setHistoryLoading(false)
    }
  }

  const compare = useMutation({
    mutationFn: () => api.diff(leftId!, rightId!, mode),
    onSuccess: (r) => {
      setResult(r)
      if (r.warnings?.length) r.warnings.forEach(w => message.warning(w))
    },
    onError: (e: any) => message.error(e?.message || '对比失败'),
  })

  const left = result?.left
  const right = result?.right
  const leftData = left ? parseMaybe(mode === 'canonical' ? left.canonical : left.body) : null
  const rightData = right ? parseMaybe(mode === 'canonical' ? right.canonical : right.body) : null

  const leftHl: HighlightMap = {}
  const rightHl: HighlightMap = {}
  for (const c of result?.result.changes || []) {
    if (c.op === 'remove') leftHl[c.path] = 'remove'
    else if (c.op === 'add') rightHl[c.path] = 'add'
    else { leftHl[c.path] = 'replace'; rightHl[c.path] = 'replace' }
  }

  const categoryData = Object.entries(result?.result.summary.byCategory || {}).sort((a, b) => b[1] - a[1])

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="左右 JSON 对比"
        description="wire 模式对比协议原生请求（如 Gemini 的 systemInstruction / contents / tools / generationConfig / cachedContent / safetySettings）；canonical 模式对比协议无关的逻辑请求，适合跨协议比较。"
        action={<Button size="small" icon={<HistoryOutlined />} loading={historyLoading} onClick={loadHistory}>加载历史快照</Button>}
      />
      <div className="panel" style={{ marginBottom: 12 }}>
        <Row gutter={[12, 10]} align="middle">
          <Col xs={24} lg={8}>
            <div className="form-hint">左侧快照</div>
            <Select
              showSearch optionFilterProp="label" value={leftId} onChange={setLeftId} style={{ width: '100%' }}
              options={options} placeholder="选择左侧快照"
            />
          </Col>
          <Col xs={24} lg={8}>
            <div className="form-hint">右侧快照</div>
            <Select
              showSearch optionFilterProp="label" value={rightId} onChange={setRightId} style={{ width: '100%' }}
              options={options} placeholder="选择右侧快照"
            />
          </Col>
          <Col xs={12} lg={4}>
            <div className="form-hint">对比模式</div>
            <Segmented block value={mode} onChange={v => setMode(v as any)} options={[{ label: 'Wire', value: 'wire' }, { label: 'Canonical', value: 'canonical' }]} />
          </Col>
          <Col xs={12} lg={4}>
            <Button type="primary" block icon={<DiffOutlined />} loading={compare.isPending} disabled={!leftId || !rightId || leftId === rightId} onClick={() => compare.mutate()}>
              开始对比
            </Button>
          </Col>
        </Row>
        {leftId && rightId && leftId === rightId && <Alert style={{ marginTop: 8 }} type="warning" showIcon message="左右选择的是同一份快照。" />}
      </div>

      {!result ? (
        <EmptyPanel text={options.length ? '选择两份快照后点击“开始对比”。' : '当前运行还没有快照。'} />
      ) : (
        <>
          <div className="kpi-grid" style={{ marginBottom: 12 }}>
            <Kpi label="差异总数" value={result.result.summary.total} />
            <Kpi label="新增" value={result.result.summary.added} color="#16a34a" />
            <Kpi label="删除" value={result.result.summary.removed} color="#dc2626" />
            <Kpi label="替换" value={result.result.summary.replaced} color="#d97706" />
            <Kpi label="高风险" value={result.result.summary.bySeverity?.high || 0} color="#dc2626" />
            <Kpi label="左 SHA" value={<span className="small-mono">{result.result.leftSha.slice(0, 12)}…</span>} />
            <Kpi label="右 SHA" value={<span className="small-mono">{result.result.rightSha.slice(0, 12)}…</span>} />
          </div>
          {!!result.warnings?.length && <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={result.warnings.join(' ')} />}

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: 14, marginBottom: 12 }}>
            <BarChart
              title="差异分类分布"
              horizontal
              categories={categoryData.map(([k]) => categoryLabel(k))}
              series={[{ name: '差异数', data: categoryData.map(([, v]) => v), color: '#7c3aed' }]}
              height={240}
            />
            <div className="panel">
              <div className="panel-title">关键分区指纹</div>
              <Table
                size="small"
                rowKey="name"
                pagination={false}
                dataSource={result.result.sections || []}
                columns={[
                  { title: '分区', dataIndex: 'label', width: 130 },
                  { title: '左侧', dataIndex: 'left', render: (v: string) => <span className="small-mono">{v ? `${v.slice(0, 12)}…` : '-'}</span> },
                  { title: '右侧', dataIndex: 'right', render: (v: string) => <span className="small-mono">{v ? `${v.slice(0, 12)}…` : '-'}</span> },
                  { title: '结果', dataIndex: 'changed', width: 80, render: (v: boolean) => v ? <Tag color="red">已变化</Tag> : <Tag color="green">一致</Tag> },
                ]}
              />
            </div>
          </div>

          <div className="diff-grid" style={{ marginBottom: 12 }}>
            <div className="diff-pane">
              <div className="diff-pane-title">
                <span>左侧 · #{left?.id} · {left?.targetLabel} / {left?.model}</span>
                <span className="muted">{left?.caseName} · rep{left?.repetition}</span>
              </div>
              {leftData ? <JsonTree data={leftData} highlight={leftHl} maxHeight={520} /> : <JsonBlock value={left?.body} maxHeight={520} />}
            </div>
            <div className="diff-pane">
              <div className="diff-pane-title">
                <span>右侧 · #{right?.id} · {right?.targetLabel} / {right?.model}</span>
                <span className="muted">{right?.caseName} · rep{right?.repetition}</span>
              </div>
              {rightData ? <JsonTree data={rightData} highlight={rightHl} maxHeight={520} /> : <JsonBlock value={right?.body} maxHeight={520} />}
            </div>
          </div>

          <div className="panel" style={{ marginBottom: 12 }}>
            <div className="panel-title">
              差异明细（JSONPath）
              <Space style={{ marginLeft: 8 }}>
                <Button size="small" icon={<DownloadOutlined />} onClick={() => download(api.exportDiffUrl(leftId!, rightId!, mode, 'html'))}>导出 HTML</Button>
                <Button size="small" icon={<DownloadOutlined />} onClick={() => download(api.exportDiffUrl(leftId!, rightId!, mode, 'json'))}>导出 JSON</Button>
              </Space>
            </div>
            <Table
              rowKey={(r, i) => `${r.path}-${i}`}
              size="small"
              dataSource={result.result.changes}
              pagination={{ pageSize: 30 }}
              scroll={{ x: 1200 }}
              onRow={r => ({ onClick: () => setActivePath(r.path), style: { cursor: 'pointer' } })}
              rowClassName={r => r.path === activePath ? 'ant-table-row-selected' : ''}
              columns={[
                { title: '操作', dataIndex: 'op', width: 80, render: (v: string) => <Tag color={opColor(v)}>{opLabel(v)}</Tag> },
                { title: 'JSONPath', dataIndex: 'path', width: 300, render: (v: string) => <span className="small-mono">{v}</span> },
                { title: '分类', dataIndex: 'category', width: 150, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
                { title: '级别', dataIndex: 'severity', width: 80, render: (v: string) => <Tag color={severityColor(v)}>{severityLabel(v)}</Tag> },
                {
                  title: '修改前', dataIndex: 'before', width: 260,
                  render: (v: any) => <Typography.Text delete={false} style={{ fontSize: 12 }}>{v === undefined ? '-' : truncate(typeof v === 'string' ? v : JSON.stringify(v), 200)}</Typography.Text>,
                },
                {
                  title: '修改后', dataIndex: 'after', width: 260,
                  render: (v: any) => <Typography.Text style={{ fontSize: 12 }}>{v === undefined ? '-' : truncate(typeof v === 'string' ? v : JSON.stringify(v), 200)}</Typography.Text>,
                },
              ]}
            />
            {result.result.summary.truncated && <Alert style={{ marginTop: 10 }} type="warning" showIcon message="差异数量超过上限，已截断；可导出完整 JSON 报告。" />}
          </div>

          <div className="panel">
            <div className="panel-title">快照元信息</div>
            <Row gutter={[16, 8]}>
              <Col xs={24} lg={12}>
                <div className="muted">左侧</div>
                <div>目标 {left?.targetLabel} / {left?.targetName} · 模型 {left?.model}</div>
                <div className="small-mono">请求 ID {left?.requestId}</div>
                <div className="small-mono">SHA-256 {left?.bodySha256}</div>
                <div className="muted">{fmtTime(left?.requestTimeMs)} · {fmtBytes(left?.bodySize)}</div>
                <CopyButton text={mode === 'canonical' ? left?.canonical : left?.body} label="复制左侧 JSON" />
              </Col>
              <Col xs={24} lg={12}>
                <div className="muted">右侧</div>
                <div>目标 {right?.targetLabel} / {right?.targetName} · 模型 {right?.model}</div>
                <div className="small-mono">请求 ID {right?.requestId}</div>
                <div className="small-mono">SHA-256 {right?.bodySha256}</div>
                <div className="muted">{fmtTime(right?.requestTimeMs)} · {fmtBytes(right?.bodySize)}</div>
                <CopyButton text={mode === 'canonical' ? right?.canonical : right?.body} label="复制右侧 JSON" />
              </Col>
            </Row>
          </div>
        </>
      )}
    </div>
  )
}
