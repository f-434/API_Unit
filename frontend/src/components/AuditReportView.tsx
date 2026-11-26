import ScrollTable from './ScrollTable'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '../pagination'
import { Button, Popconfirm, Space, Tag, Typography, message } from 'antd'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import type { AuditCell, AuditReport } from '../types'
import { Kpi, EmptyPanel, NotesCollapse, ResponseModelCell, VerdictTag } from './common'
import {
  auditCellStats, fmtCost, fmtMs, fmtNum, fmtPct, fmtInt, requestSuccessRate, targetAuditStats,
} from '../format'
import { BarChart } from './charts'

function CellView({ c, onSave }: { c?: AuditCell; onSave?: (tokens: number) => void }) {
  if (!c) return <span className="muted">-</span>
  const manual = c.localSource === 'manual'
  const tokens = Math.round(c.medianInput)
  return (
    <div style={{ lineHeight: 1.55 }}>
      <Space size={4} wrap>
        <b>{fmtNum(c.medianInput, 0)}</b>
        <span className="muted">tok</span>
        <VerdictTag verdict={c.status} />
      </Space>
      <div className="muted" style={{ fontSize: 11.5 }}>
        Δ {fmtPct(c.deltaPct)} · 波动 {fmtPct(c.spreadPct)}
        {c.localTokens > 0 && c.status !== 'error' && c.success > 0 && c.avgInput > 0 && <> · {manual ? '手工' : '本地'}偏差 {fmtPct(c.overheadPct)}</>}
      </div>
      <div className="muted" style={{ fontSize: 11.5 }}>
        {c.success}/{c.count} 成功 · {fmtMs(c.avgLatencyMs)}
        {c.missingUsage > 0 && <span style={{ color: '#d97706' }}> · 缺 usage {c.missingUsage}</span>}
        {onSave && tokens > 0 && c.success > 0 && (
          <Popconfirm
            title={`将 ${tokens} tok 存为该提示词在 ${c.tokenFamily} 的基准？`}
            description="会覆盖该路径下已有的校准值。"
            onConfirm={() => onSave(tokens)}
          >
            <Button type="link" size="small" style={{ padding: 0, height: 'auto', fontSize: 11.5 }}>存基准</Button>
          </Popconfirm>
        )}
      </div>
    </div>
  )
}

export default function AuditReportView({ report, showCharts = true, hideSummary = false }: {
  report?: AuditReport
  showCharts?: boolean
  /** 测试详情页顶部已展示同样的汇总，避免重复。 */
  hideSummary?: boolean
}) {
  const qc = useQueryClient()
  const backfill = useMutation({
    mutationFn: (v: { promptId: string; family: string; tokens: number }) =>
      api.setPromptTokenReference(v.promptId, { family: v.family, tokens: v.tokens }),
    onSuccess: () => {
      message.success('已写入提示词基准')
      qc.invalidateQueries({ queryKey: ['prompts'] })
      // 模型级回填会自动登记到口径树，刷新设置页与提示词编辑器的口径。
      qc.invalidateQueries({ queryKey: ['meta'] })
      qc.invalidateQueries({ queryKey: ['tokenFamilies'] })
    },
    onError: (e: any) => message.error(e?.message || '写入失败'),
  })
  if (!report) return <EmptyPanel text="审计报告尚未生成（测试运行中或已中断）。" />
  const audit = auditCellStats(report)
  const reqRate = requestSuccessRate(report)
  const targetCols = report.targets.map(t => {
    const actual = (t.responseModels || []).filter(m => m && m !== t.model)
    return {
      title: (
        <div>
          <div>{t.targetLabel}</div>
          <div className="muted" style={{ fontWeight: 400, fontSize: 11 }}>{t.model}</div>
          {actual.length > 0 && (
            <div style={{ fontWeight: 400, fontSize: 11, color: '#d97706' }}>响应 {actual.join('、')}</div>
          )}
        </div>
      ),
      key: `t_${t.targetIndex}`,
      width: 220,
      render: (_: any, row: any) => {
        const cell = row.cells.find((c: AuditCell) => c.targetIndex === t.targetIndex)
        const family = cell?.tokenFamily
        return (
          <CellView
            c={cell}
            onSave={family
              ? tokens => backfill.mutate({ promptId: row.promptId, family, tokens })
              : undefined}
          />
        )
      },
    }
  })

  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        {!hideSummary && (
          <Kpi
            label="审计成功率"
            value={audit.rate === undefined ? '-' : fmtPct(audit.rate)}
            color={audit.rate === undefined ? undefined : audit.rate >= 100 ? '#16a34a' : '#d97706'}
          />
        )}
        <Kpi
          label="审计单元格"
          value={audit.total ? `${fmtInt(audit.normal)} / ${fmtInt(audit.total)}` : '-'}
          color={audit.total && audit.normal < audit.total ? '#d97706' : undefined}
        />
        {!hideSummary && <Kpi label="请求成功率" value={fmtPct(reqRate)} />}
        {!hideSummary && <Kpi label="总请求" value={fmtInt(report.totalRequests)} />}
        {!hideSummary && <Kpi label="成功" value={fmtInt(report.success)} color="#16a34a" />}
        {!hideSummary && <Kpi label="错误" value={fmtInt(report.errors)} color={report.errors ? '#dc2626' : undefined} />}
        <Kpi label="缺少 usage" value={fmtInt(report.missingUsage)} color={report.missingUsage ? '#d97706' : undefined} />
        <Kpi label="异常单元格" value={report.anomalies.length} color={report.anomalies.length ? '#dc2626' : '#16a34a'} />
        <Kpi label="本地可比样本" value={fmtInt(report.localComparable)} />
        <Kpi label="判定阈值" value={fmtPct(report.threshold, 0)} />
        <Kpi label="基线" value={report.baselineMethod === 'median' ? '中位数' : '最小中位数'} />
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">目标汇总</div>
        <ScrollTable
          rowKey="targetIndex"
          size="small"
          pagination={false}
          dataSource={report.targets}
          columns={[
            { title: '目标', dataIndex: 'targetLabel', width: 160 },
            { title: '模型', dataIndex: 'model', render: (v: string) => <span className="mono">{v}</span> },
            {
              title: '响应模型', width: 190,
              render: (_: any, t: any) => <ResponseModelCell requested={t.model} models={t.responseModels} />,
            },
            { title: '请求', dataIndex: 'count', width: 80 },
            { title: '成功', dataIndex: 'success', width: 80 },
            {
              title: '请求成功率', width: 110,
              render: (_: any, t: any) => <span>{fmtPct(t.count ? (t.success / t.count) * 100 : 0)}</span>,
            },
            {
              title: '审计成功率', width: 110,
              render: (_: any, t: any) => {
                const s = targetAuditStats(report, t.targetIndex)
                return <span style={{ color: s.rate !== undefined && s.rate < 100 ? '#d97706' : undefined }}>{s.rate === undefined ? '-' : fmtPct(s.rate)}</span>
              },
            },
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
        <ScrollTable
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

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">异常明细</div>
        {report.anomalies.length === 0 ? (
          <span className="muted">未发现超过阈值的异常</span>
        ) : (
          <ScrollTable
            rowKey={(r, i) => `${r.promptId}-${r.targetLabel}-${i}`}
            size="small"
            dataSource={report.anomalies}
            pagination={{ pageSize: DEFAULT_PAGE_SIZE, pageSizeOptions: PAGE_SIZE_OPTIONS, showSizeChanger: true }}
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

      <NotesCollapse notes={report.notes} />
    </div>
  )
}
