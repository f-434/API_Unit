import { Alert, Space, Table, Tag } from 'antd'
import type { InjectionReport, Live } from '../types'
import { EmptyPanel, Kpi, SeverityTag, VerdictTag } from './common'
import { BarChart, DonutChart } from './charts'
import { categoryLabel, fmtCost, fmtPct, fmtInt, verdictLabel } from '../format'

function liveCounts(live?: Live) {
  const ex = live?.extra || {}
  const success = Number(ex.verdict_success || 0)
  const blocked = Number(ex.verdict_blocked || 0)
  const unclear = Number(ex.verdict_unclear || 0)
  const errors = Number(ex.verdict_error || 0)
  const denom = success + blocked + unclear
  return { success, blocked, unclear, errors, total: success + blocked + unclear + errors, asr: denom ? success / denom * 100 : 0, judgeTokens: Number(ex.judge_tokens || 0) }
}

export default function InjectionReportView({ report, live }: { report?: InjectionReport; live?: Live }) {
  const counts = report ? {
    success: report.overall.success, blocked: report.overall.blocked, unclear: report.overall.unclear,
    errors: report.overall.errors, total: report.overall.total, asr: report.overall.asr,
    judgeTokens: report.judgeTokens,
  } : liveCounts(live)

  if (!report && !live) return <EmptyPanel text="暂无测试数据。" />

  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="ASR（攻击成功率）" value={fmtPct(counts.asr)} color={counts.asr > 0 ? '#dc2626' : '#16a34a'} />
        <Kpi label="注入成功" value={fmtInt(counts.success)} color={counts.success ? '#dc2626' : undefined} />
        <Kpi label="已防御" value={fmtInt(counts.blocked)} color="#16a34a" />
        <Kpi label="无法判定" value={fmtInt(counts.unclear)} color={counts.unclear ? '#d97706' : undefined} />
        <Kpi label="执行失败" value={fmtInt(counts.errors)} />
        <Kpi label="总测试数" value={fmtInt(counts.total)} />
        <Kpi label="裁判 Token" value={fmtInt(counts.judgeTokens)} />
        {report && <Kpi label="裁判成本" value={fmtCost(report.judgeCost)} />}
        {report && <Kpi label="规则直接判定" value={fmtInt(report.ruleDecisive)} />}
        {report && <Kpi label="LLM 裁判次数" value={fmtInt(report.llmJudged)} />}
      </div>

      {live?.status === 'running' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 14 }}
          message={live.phase || '注入测试运行中'}
          description={<Space wrap><span>已完成 {live.completed} / {live.planned || '—'}</span><span>当前模型 {live.currentModel || '-'}</span></Space>}
        />
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))', gap: 14, marginBottom: 14 }}>
        <div className="panel">
          <div className="panel-title">判定分布</div>
          <DonutChart
            title=""
            height={240}
            data={[
              { name: '注入成功', value: counts.success, color: '#dc2626' },
              { name: '已防御', value: counts.blocked, color: '#16a34a' },
              { name: '无法判定', value: counts.unclear, color: '#d97706' },
              { name: '执行失败', value: counts.errors, color: '#94a3b8' },
            ]}
          />
        </div>
        {report && report.targets.length > 0 && (
          <BarChart
            title="各目标 ASR"
            categories={report.targets.map(t => `${t.targetLabel} / ${t.model}`)}
            series={[{ name: 'ASR %', data: report.targets.map(t => Number(t.asr.toFixed(2))), color: '#dc2626' }]}
            height={240}
          />
        )}
      </div>

      {report?.targets?.length ? (
        <div className="panel" style={{ marginBottom: 14 }}>
          <div className="panel-title">目标风险</div>
          <Table
            rowKey="targetIndex"
            size="small"
            pagination={false}
            dataSource={report.targets}
            columns={[
              { title: '目标', dataIndex: 'targetLabel', width: 170 },
              { title: '模型', dataIndex: 'model', render: (v: string) => <span className="mono">{v}</span> },
              { title: '测试数', dataIndex: 'total', width: 90 },
              { title: '注入成功', dataIndex: 'success', width: 100, render: (v: number) => <span style={{ color: v ? '#dc2626' : undefined }}>{v}</span> },
              { title: '已防御', dataIndex: 'blocked', width: 90 },
              { title: '无法判定', dataIndex: 'unclear', width: 100 },
              { title: '执行失败', dataIndex: 'errors', width: 100 },
              { title: 'ASR', dataIndex: 'asr', width: 100, render: (v: number) => <b style={{ color: v > 0 ? '#dc2626' : '#16a34a' }}>{fmtPct(v)}</b> },
            ]}
          />
        </div>
      ) : null}

      {report?.categories?.length ? (
        <div className="panel" style={{ marginBottom: 14 }}>
          <div className="panel-title">分类风险</div>
          <Table
            rowKey="category"
            size="small"
            pagination={false}
            dataSource={report.categories}
            columns={[
              { title: '分类', dataIndex: 'category', width: 190, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
              { title: '测试数', dataIndex: 'total', width: 90 },
              { title: '注入成功', dataIndex: 'success', width: 100 },
              { title: '已防御', dataIndex: 'blocked', width: 90 },
              { title: '无法判定', dataIndex: 'unclear', width: 100 },
              { title: 'ASR', dataIndex: 'asr', width: 100, render: (v: number) => <b style={{ color: v > 0 ? '#dc2626' : '#16a34a' }}>{fmtPct(v)}</b> },
            ]}
          />
        </div>
      ) : null}

      {report?.cases?.length ? (
        <div className="panel" style={{ marginBottom: 14 }}>
          <div className="panel-title">用例结果</div>
          <Table
            rowKey="attackId"
            size="small"
            dataSource={report.cases}
            pagination={{ pageSize: 20 }}
            expandable={{
              expandedRowRender: row => (
                <Table
                  rowKey="targetIndex"
                  size="small"
                  pagination={false}
                  dataSource={row.targets}
                  columns={[
                    { title: '目标', dataIndex: 'targetLabel', width: 160 },
                    { title: '模型', dataIndex: 'model', render: (v: string) => <span className="mono">{v}</span> },
                    { title: '判定', dataIndex: 'lastVerdict', width: 110, render: (v: string) => <VerdictTag verdict={v} /> },
                    { title: '次数', dataIndex: 'total', width: 80 },
                    { title: 'ASR', dataIndex: 'asr', width: 90, render: (v: number) => fmtPct(v) },
                    { title: '理由', dataIndex: 'lastReason' },
                  ]}
                />
              ),
            }}
            columns={[
              { title: '用例', dataIndex: 'attackName', width: 260 },
              { title: '分类', dataIndex: 'category', width: 150, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
              { title: '严重度', dataIndex: 'severity', width: 95, render: (v: string) => <SeverityTag severity={v} /> },
              { title: '次数', width: 85, render: (_: any, r: any) => r.overall.total },
              { title: '注入成功', width: 100, render: (_: any, r: any) => <span style={{ color: r.overall.success ? '#dc2626' : undefined }}>{r.overall.success}</span> },
              { title: '已防御', width: 90, render: (_: any, r: any) => r.overall.blocked },
              { title: '无法判定', width: 100, render: (_: any, r: any) => r.overall.unclear },
              { title: 'ASR', dataIndex: ['overall', 'asr'], width: 100, render: (v: number) => <b style={{ color: v > 0 ? '#dc2626' : '#16a34a' }}>{fmtPct(v)}</b> },
            ] as any}
          />
        </div>
      ) : null}

      {report?.notes && (
        <Alert
          type="info"
          showIcon
          message="判定口径"
          description={<ul style={{ margin: 0, paddingLeft: 18 }}>{report.notes.map((n, i) => <li key={i}>{n}</li>)}</ul>}
        />
      )}
      {!report && live && (
        <Alert type="info" showIcon message="测试运行中" description="实时 ASR 来自当前判定计数；完整用例/分类报告在测试完成后生成。" />
      )}
    </div>
  )
}
