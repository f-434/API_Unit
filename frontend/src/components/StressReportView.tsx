import { Alert, Space, Table, Tag, Tooltip } from 'antd'
import type { Aggregate, HistBucket, Live, ModelAggregate, ModelLive, SeriesPoint } from '../types'
import { EmptyPanel, Kpi, ProtocolTag } from './common'
import { BarChart, DonutChart, SeriesCharts } from './charts'
import { errorTypeLabel, fmtCost, fmtInt, fmtMs, fmtNum, fmtPct, fmtTime, statusLabel, truncate } from '../format'

function statusColor(status: number) {
  if (status === 0) return '#94a3b8'
  if (status < 300) return '#16a34a'
  if (status < 400) return '#0ea5e9'
  if (status < 500) return '#d97706'
  return '#dc2626'
}

export default function StressReportView({ aggregate, models, live }: {
  aggregate?: Aggregate
  models?: ModelAggregate[]
  live?: Live
}) {
  const series: SeriesPoint[] = aggregate?.series || live?.series || []
  const hasData = !!aggregate || !!live
  if (!hasData) return <EmptyPanel text="暂无压测数据。" />

  const total = aggregate?.total ?? live?.completed ?? 0
  const success = aggregate?.success ?? live?.success ?? 0
  const failed = aggregate?.failed ?? live?.failed ?? 0
  const canceled = aggregate?.canceled ?? live?.canceled ?? 0
  const successRate = aggregate?.successRate ?? live?.successRate ?? 0
  const qps = aggregate?.qps ?? live?.qps ?? 0
  const elapsed = aggregate?.elapsedMs ?? live?.elapsedMs ?? 0
  const latency = aggregate?.latency || live?.latency
  const ttft = aggregate?.ttft || live?.ttft
  const tpot = aggregate?.tpot || live?.tpot
  const tokens = aggregate?.tokens || live?.tokens
  const statusCodes = aggregate?.statusCodes || live?.statusCodes || []
  const errors = aggregate?.errors || live?.errors || []
  const histogram: HistBucket[] = aggregate?.histogram || live?.histogram || []
  const modelLive: ModelLive[] = live?.models || []

  const modelRows = models && models.length ? models : modelLive.map(m => ({
    model: m.model, targetName: m.targetName, protocol: m.protocol,
    total: m.completed, success: m.success, failed: m.failed, successRate: m.successRate,
    qps: m.qps, latency: { avg: m.avgLatencyMs, p95: 0 }, ttft: { avg: m.ttftMs },
    tokens: { prompt: 0, completion: 0, cost: 0 },
  })) as any[]

  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="总请求" value={fmtInt(total)} />
        <Kpi label="成功" value={fmtInt(success)} color="#16a34a" />
        <Kpi label="失败" value={fmtInt(failed)} color={failed ? '#dc2626' : undefined} />
        <Kpi label="取消" value={fmtInt(canceled)} />
        <Kpi label="成功率" value={fmtPct(successRate)} />
        <Kpi label="QPS" value={fmtNum(qps, 2)} suffix="req/s" />
        <Kpi label="平均延迟" value={fmtMs(latency?.avg)} />
        <Kpi label="P95 延迟" value={fmtMs(latency?.p95)} />
        <Kpi label="平均 TTFT" value={fmtMs(ttft?.avg)} />
        <Kpi label="平均 TPOT" value={fmtMs(tpot?.avg)} />
        <Kpi label="平均输出速度" value={fmtNum(tokens?.avgTokensPerSec, 1)} suffix="tok/s" />
        <Kpi label="输出 Token" value={fmtInt(tokens?.completion)} />
        <Kpi label="成本" value={fmtCost(tokens?.cost)} />
        <Kpi label="实际并发" value={live ? fmtInt(live.activeConcurrency) : '-'} />
      </div>

      {live && live.status === 'running' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 14 }}
          message={live.phase || '压测运行中'}
          description={<Space wrap><span>已完成 {live.completed} / {live.planned || '—'}</span><span>当前模型 {live.currentModel || '-'}</span><Tag color="processing">{statusLabel(live.status)}</Tag></Space>}
        />
      )}

      <div style={{ marginBottom: 14 }}>
        <SeriesCharts series={series} />
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">模型对比</div>
        <Table
          rowKey="model"
          size="small"
          pagination={false}
          dataSource={modelRows}
          columns={[
            { title: '模型', dataIndex: 'model', width: 190, render: (v: string) => <span className="mono" style={{ whiteSpace: 'nowrap' }}>{v}</span> },
            { title: '上游', dataIndex: 'targetName', width: 140 },
            { title: '协议', dataIndex: 'protocol', width: 110, render: (v: string) => <ProtocolTag protocol={v} /> },
            { title: '请求', dataIndex: 'total', width: 90 },
            { title: '成功率', dataIndex: 'successRate', width: 100, render: (v: number) => fmtPct(v) },
            { title: '平均延迟', width: 110, render: (_: any, r: any) => fmtMs(r.latency?.avg) },
            { title: 'P95', width: 100, render: (_: any, r: any) => r.latency?.p95 ? fmtMs(r.latency.p95) : '-' },
            { title: 'TTFT', width: 100, render: (_: any, r: any) => fmtMs(r.ttft?.avg) },
            { title: 'QPS', dataIndex: 'qps', width: 90, render: (v: number) => fmtNum(v, 2) },
            { title: 'Prompt', width: 100, render: (_: any, r: any) => fmtInt(r.tokens?.prompt) },
            { title: 'Completion', width: 110, render: (_: any, r: any) => fmtInt(r.tokens?.completion) },
            { title: '成本', width: 100, render: (_: any, r: any) => fmtCost(r.tokens?.cost) },
          ] as any}
          scroll={{ x: 1200 }}
        />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))', gap: 14, marginBottom: 14 }}>
        <div className="panel">
          <div className="panel-title">状态码分布</div>
          <DonutChart
            title=""
            height={230}
            data={statusCodes.map(s => ({ name: s.status === 0 ? '网络/无响应' : `HTTP ${s.status}`, value: s.count, color: statusColor(s.status) }))}
          />
        </div>
        <div className="panel">
          <div className="panel-title">延迟直方图</div>
          {histogram.length ? (
            <BarChart title="" categories={histogram.map(b => b.label)} series={[{ name: '请求数', data: histogram.map(b => b.count), color: '#7c3aed' }]} height={230} />
          ) : <span className="muted">暂无数据</span>}
        </div>
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">错误分组</div>
        {errors.length ? (
          <Table
            rowKey={(r, i) => `${r.type}-${i}`}
            size="small"
            dataSource={errors}
            pagination={false}
            columns={[
              { title: '类型', dataIndex: 'type', width: 120, render: (v: string) => <Tag color="red">{errorTypeLabel(v)}</Tag> },
              { title: '次数', dataIndex: 'count', width: 80 },
              { title: '状态码', dataIndex: 'statuses', width: 120, render: (v: number[]) => (v || []).map(s => <Tag key={s}>{s === 0 ? '网络' : s}</Tag>) },
              { title: '模型', dataIndex: 'models', width: 180, render: (v: string[]) => (v || []).map(m => <Tag key={m}>{m}</Tag>) },
              { title: '消息', dataIndex: 'message', render: (v: string) => <Tooltip title={v}><span className="small-mono">{truncate(v, 160)}</span></Tooltip> },
            ]}
          />
        ) : <Alert type="success" showIcon message="没有错误。" />}
      </div>

      {!!live?.recent?.length && (
        <div className="panel">
          <div className="panel-title">最近请求</div>
          <Table
            rowKey="index"
            size="small"
            pagination={false}
            dataSource={live.recent}
            columns={[
              { title: '模型', dataIndex: 'model', width: 170 },
              { title: '状态', width: 100, render: (_: any, r: any) => <Tag color={r.ok ? 'green' : 'red'}>{r.status === 0 ? '网络' : r.status}</Tag> },
              { title: '延迟', width: 90, render: (_: any, r: any) => fmtMs(r.latencyMs) },
              { title: 'TTFT', width: 90, render: (_: any, r: any) => fmtMs(r.ttftMs) },
              { title: 'Token', width: 100, render: (_: any, r: any) => `${r.promptTokens}/${r.completionTokens}` },
              { title: '开始', width: 100, render: (_: any, r: any) => fmtTime(r.startedAt) },
              { title: '错误/预览', render: (_: any, r: any) => r.ok ? <span className="muted">{truncate(r.outputPreview, 80)}</span> : <span style={{ color: '#dc2626' }}>{truncate(r.errorMessage, 100)}</span> },
            ]}
          />
        </div>
      )}
    </div>
  )
}
