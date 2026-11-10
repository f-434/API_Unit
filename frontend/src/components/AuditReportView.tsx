import { Alert, Space, Table, Tag, Tooltip, Typography } from 'antd'
import type { AuditCell, AuditReport } from '../types'
import { Kpi, EmptyPanel, VerdictTag } from './common'
import { fmtCost, fmtMs, fmtNum, fmtPct, fmtInt } from '../format'
import { BarChart } from './charts'

function CellView({ c }: { c?: AuditCell }) {
  if (!c) return <span className="muted">-</span>
  return (
    <div style={{ lineHeight: 1.55 }}>
      <Space size={4} wrap>
        <b>{fmtNum(c.medianInput, 0)}</b>
        <span className="muted">tok</span>
        <VerdictTag verdict={c.status} />
      </Space>
      <div className="muted" style={{ fontSize: 11.5 }}>
        Δ {fmtPct(c.deltaPct)} · 波动 {fmtPct(c.spreadPct)}
        {c.localTokens > 0 && <> · 本地偏差 {fmtPct(c.overheadPct)}</>}
      </div>
      <div className="muted" style={{ fontSize: 11.5 }}>
        {c.success}/{c.count} 成功 · {fmtMs(c.avgLatencyMs)}
        {c.missingUsage > 0 && <span style={{ color: '#d97706' }}> · 缺 usage {c.missingUsage}</span>}
      </div>
    </div>
  )
}

export default function AuditReportView({ report, showCharts = true }: { report?: AuditReport; showCharts?: boolean }) {
  if (!report) return <EmptyPanel text="审计报告尚未生成（测试运行中或已中断）。" />
  const targetCols = report.targets.map(t => ({
    title: (
      <div>
        <div>{t.targetLabel}</div>
        <div className="muted" style={{ fontWeight: 400, fontSize: 11 }}>{t.model}</div>
      </div>
    ),
    key: `t_${t.targetIndex}`,
    width: 220,
    render: (_: any, row: any) => {
      const cell = row.cells.find((c: AuditCell) => c.targetIndex === t.targetIndex)
      return <CellView c={cell} />
    },
  }))

  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="总请求" value={fmtInt(report.totalRequests)} />
        <Kpi label="成功" value={fmtInt(report.success)} color="#16a34a" />
        <Kpi label="错误" value={fmtInt(report.errors)} color={report.errors ? '#dc2626' : undefined} />
        <Kpi label="缺少 usage" value={fmtInt(report.missingUsage)} color={report.missingUsage ? '#d97706' : undefined} />
        <Kpi label="异常单元格" value={report.anomalies.length} color={report.anomalies.length ? '#dc2626' : '#16a34a'} />
        <Kpi label="本地可比样本" value={fmtInt(report.localComparable)} />
        <Kpi label="判定阈值" value={fmtPct(report.threshold, 0)} />
        <Kpi label="基线" value={report.baselineMethod === 'median' ? '中位数' : '最小中位数'} />
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">目标汇总</div>
        <Table
          rowKey="targetIndex"
          size="small"
          pagination={false}
          dataSource={report.targets}
          columns={[
            { title: '目标', dataIndex: 'targetLabel', width: 160 },
            { title: '模型', dataIndex: 'model', render: (v: string) => <span className="mono">{v}</span> },
            { title: '请求', dataIndex: 'count', width: 90 },
            { title: '成功', dataIndex: 'success', width: 90 },
            { title: '中位输入', dataIndex: 'medianInput', width: 110, render: (v: number) => fmtNum(v, 0) },
            { title: '本地 Token', dataIndex: 'localTokens', width: 110, render: (v: number) => fmtNum(v, 1) },
            { title: '本地偏差', dataIndex: 'overheadPct', width: 110, render: (v: number) => <span style={{ color: v > report.threshold ? '#dc2626' : undefined }}>{fmtPct(v)}</span> },
            { title: '异常', dataIndex: 'anomalies', width: 80, render: (v: number) => v ? <Tag color="red">{v}</Tag> : <Tag color="green">0</Tag> },
            { title: '成本', dataIndex: 'cost', width: 100, render: (v: number) => fmtCost(v) },
          ]}
        />
      </div>

      {showCharts && report.targets.length > 1 && (
        <div className="panel" style={{ marginBottom: 14 }}>
          <BarChart
            title="各目标中位输入 Token"
            categories={report.targets.map(t => t.targetLabel)}
            series={[{ name: '中位输入', data: report.targets.map(t => Math.round(t.medianInput)), color: '#2563eb' }]}
            height={220}
          />
        </div>
      )}

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">提示词 × 目标矩阵</div>
        <div className="scroll-x">
          <Table
            rowKey="promptId"
            size="small"
            pagination={false}
            dataSource={report.prompts}
            columns={[
              { title: '提示词', dataIndex: 'promptName', width: 190, fixed: 'left' },
              { title: '基线 Token', dataIndex: 'baselineInput', width: 110, fixed: 'left', render: (v: number) => fmtNum(v, 0) },
              ...targetCols,
            ] as any}
            scroll={{ x: 400 + targetCols.length * 220 }}
          />
        </div>
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">异常明细</div>
        {report.anomalies.length === 0 ? (
          <Alert type="success" showIcon message="未发现超过阈值的异常。" />
        ) : (
          <Table
            rowKey={(r, i) => `${r.promptId}-${r.targetLabel}-${i}`}
            size="small"
            dataSource={report.anomalies}
            pagination={{ pageSize: 20 }}
            columns={[
              { title: '提示词', dataIndex: 'promptName', width: 190 },
              { title: '目标', width: 190, render: (_: any, r) => <span>{r.targetLabel} <span className="muted small-mono">{r.model}</span></span> },
              { title: '状态', dataIndex: 'status', width: 110, render: (v: string) => <VerdictTag verdict={v} /> },
              { title: 'Δ 基线', dataIndex: 'deltaPct', width: 100, render: (v: number) => fmtPct(v) },
              { title: '重复波动', dataIndex: 'spreadPct', width: 110, render: (v: number) => fmtPct(v) },
              { title: '本地偏差', dataIndex: 'overheadPct', width: 110, render: (v: number) => fmtPct(v) },
              {
                title: '原因', dataIndex: 'reasons',
                render: (v: string[]) => <Typography.Text style={{ fontSize: 12 }}>{(v || []).join('；')}</Typography.Text>,
              },
            ]}
          />
        )}
      </div>

      <Alert
        type="info"
        showIcon
        message="判定口径"
        description={<ul style={{ margin: 0, paddingLeft: 18 }}>{(report.notes || []).map((n, i) => <li key={i}>{n}</li>)}</ul>}
      />
    </div>
  )
}
