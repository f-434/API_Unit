import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Descriptions, Dropdown, Popconfirm, Space, Spin, Tabs, Tag, message,
} from 'antd'
import {
  ArrowLeftOutlined, DeleteOutlined, DownloadOutlined, ReloadOutlined, StopOutlined,
} from '@ant-design/icons'
import { api, subscribeRun } from '../api'
import type { InjectionReport, Live, RequestDiffReport } from '../types'
import {
  kindLabel, kindColor, fmtInt, fmtPct, fmtDuration, fmtTime, fmtCost, statusLabel,
} from '../format'
import { EmptyPanel, JsonBlock, Kpi, PageHeader, StatusTag, KindTag } from '../components/common'
import { SeriesCharts } from '../components/charts'
import StressReportView from '../components/StressReportView'
import AuditReportView from '../components/AuditReportView'
import InjectionReportView from '../components/InjectionReportView'
import RunItems from '../components/RunItems'
import { DiffPanel, FindingsTable, SnapshotTable } from '../components/RequestDiffTabs'

function RunningOverview({ live }: { live?: Live }) {
  if (!live) return <EmptyPanel text="测试运行中，等待第一批数据…" />
  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="总请求" value={fmtInt(live.completed)} />
        <Kpi label="成功" value={fmtInt(live.success)} color="#16a34a" />
        <Kpi label="失败" value={fmtInt(live.failed)} color={live.failed ? '#dc2626' : undefined} />
        <Kpi label="成功率" value={fmtPct(live.successRate)} />
        <Kpi label="QPS" value={(live.qps || 0).toFixed(2)} />
        <Kpi label="实际并发" value={fmtInt(live.activeConcurrency)} />
        <Kpi label="当前模型" value={live.currentModel || '-'} />
        <Kpi label="耗时" value={fmtDuration(live.elapsedMs)} />
      </div>
      <SeriesCharts series={live.series || []} />
    </div>
  )
}

function RequestDiffOverview({ report, live, running }: { report?: RequestDiffReport; live?: Live; running: boolean }) {
  if (!report) return <RunningOverview live={live} />
  return (
    <div>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="请求快照" value={report.snapshotCount} />
        <Kpi label="隐藏指令线索" value={report.findingCount} color={report.findingCount ? '#dc2626' : '#16a34a'} />
        <Kpi label="用例数" value={report.cases.length} />
        <Kpi label="目标数" value={report.targets.length} />
        {Object.entries(report.findingsBySeverity || {}).map(([k, v]) => <Kpi key={k} label={`线索级别 ${k}`} value={v} color={k === 'high' ? '#dc2626' : k === 'medium' ? '#d97706' : '#2563eb'} />)}
      </div>
      {!!report.warnings?.length && <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={report.warnings.join('；')} />}
      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">请求用例</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))', gap: 10 }}>
          {report.cases.map(c => (
            <div key={c.id} className="case-card" style={{ marginTop: 0 }}>
              <div style={{ fontWeight: 650 }}>{c.name}</div>
              <div className="muted" style={{ fontSize: 12 }}>{c.id}</div>
              <Space wrap style={{ marginTop: 8 }}>
                <Tag>{c.messageCount} 消息</Tag>
                {c.hasHistory && <Tag color="blue">历史</Tag>}
                {c.hasTools && <Tag color="purple">Tools</Tag>}
                {c.hasMedia && <Tag color="orange">多模态</Tag>}
                {c.hasWebSearch && <Tag color="cyan">Web Search</Tag>}
                {c.hasFunctionResponse && <Tag color="magenta">Function Response</Tag>}
              </Space>
            </div>
          ))}
        </div>
      </div>
      <AuditReportView report={report.audit} />
      <Alert
        type="info"
        showIcon
        style={{ marginTop: 14 }}
        message="关于快照边界"
        description={<ul style={{ margin: 0, paddingLeft: 18 }}>{report.notes.map((n, i) => <li key={i}>{n}</li>)}</ul>}
      />
    </div>
  )
}

export default function RunDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [live, setLive] = useState<Live | undefined>()
  const [stopping, setStopping] = useState(false)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['run', id],
    queryFn: () => api.run(id),
    refetchInterval: (query) => {
      const d: any = query.state.data
      return d?.run?.status === 'running' || d?.run?.status === 'queued' ? 5000 : false
    },
  })

  useEffect(() => {
    if (data?.live) setLive(data.live)
  }, [data?.live])

  useEffect(() => {
    if (!id || !data) return
    if (data.run.status !== 'running' && data.run.status !== 'queued') return
    const sub = subscribeRun(id, {
      onLive: l => setLive(l),
      onDone: () => {
        qc.invalidateQueries({ queryKey: ['run', id] })
        qc.invalidateQueries({ queryKey: ['runs'] })
      },
    })
    return () => sub.close()
  }, [id, data?.run.status, qc])

  const kind = data?.run.kind
  const runStatus = data?.run.status
  const terminal = runStatus === 'completed' || runStatus === 'stopped' || runStatus === 'failed' || runStatus === 'interrupted'
  const status = terminal ? runStatus : (live?.status || runStatus)
  const summary = data?.summary
  const reportData = summary?.data
  const models = useMemo(() => summary?.models || [], [summary])

  const stop = async () => {
    setStopping(true)
    try {
      await api.stopRun(id)
      message.success('已发送停止指令')
      setTimeout(() => refetch(), 800)
    } catch (e: any) {
      message.error(e?.message || '停止失败')
    } finally {
      setStopping(false)
    }
  }

  const remove = async () => {
    try {
      await api.deleteRun(id)
      message.success('已删除')
      qc.invalidateQueries({ queryKey: ['runs'] })
      navigate('/runs')
    } catch (e: any) {
      message.error(e?.message || '删除失败')
    }
  }

  if (isLoading) return <div style={{ padding: 60, textAlign: 'center' }}><Spin size="large" /></div>
  if (!data) return <EmptyPanel text="测试不存在或已被清理。" />

  const isRunning = status === 'running' || status === 'queued'
  const headerKpis = (
    <div className="kpi-grid" style={{ marginBottom: 14 }}>
      <Kpi label="状态" value={<StatusTag status={status} />} />
      <Kpi label="总请求" value={fmtInt(summary?.total ?? live?.completed)} />
      <Kpi label="成功 / 失败" value={<span>{fmtInt(summary?.success ?? live?.success)} / {fmtInt(summary?.failed ?? live?.failed)}</span>} />
      <Kpi label="成功率" value={fmtPct(summary?.successRate ?? live?.successRate)} />
      {kind === 'injection' ? (
        <Kpi label="ASR" value={fmtPct(summary?.asr)} color={(summary?.asr || 0) > 0 ? '#dc2626' : undefined} />
      ) : (
        <Kpi label="QPS" value={((summary?.qps ?? live?.qps) || 0).toFixed(2)} />
      )}
      <Kpi label="耗时" value={fmtDuration(summary?.elapsedMs ?? live?.elapsedMs)} />
      <Kpi label="成本" value={fmtCost(summary?.cost)} />
    </div>
  )

  const exportMenu = {
    items: [
      { key: 'json', label: 'JSON 摘要' },
      { key: 'json-items', label: 'JSON 摘要 + 全部明细' },
      { key: 'html', label: 'HTML 报告' },
      { key: 'csv', label: 'CSV 明细' },
    ],
    onClick: ({ key }: { key: string }) => {
      if (key === 'json') return download(api.exportRunUrl(id, 'json'))
      if (key === 'json-items') return download(api.exportRunUrl(id, 'json', true))
      if (key === 'html') return download(api.exportRunUrl(id, 'html'))
      if (key === 'csv') return download(api.exportRunUrl(id, 'csv'))
    },
  }

  let tabs: any[] = []
  if (kind === 'stress') {
    tabs = [
      {
        key: 'overview', label: '实时 / 报告',
        children: <StressReportView aggregate={reportData?.aggregate} models={reportData?.models} live={live} />,
      },
      { key: 'items', label: '结果明细', children: <RunItems runId={id} kind={kind} models={models.filter(Boolean)} /> },
      { key: 'config', label: '配置', children: <JsonBlock value={data.config} maxHeight={620} /> },
    ]
  } else if (kind === 'token_audit') {
    tabs = [
      { key: 'overview', label: '审计总览', children: reportData ? <AuditReportView report={reportData} /> : <RunningOverview live={live} /> },
      { key: 'items', label: '结果明细', children: <RunItems runId={id} kind={kind} models={models.filter(Boolean)} /> },
      { key: 'raw', label: '原始报告', children: <JsonBlock value={reportData || {}} maxHeight={620} /> },
      { key: 'config', label: '配置', children: <JsonBlock value={data.config} maxHeight={620} /> },
    ]
  } else if (kind === 'request_diff') {
    tabs = [
      {
        key: 'overview', label: '审计总览',
        children: <RequestDiffOverview report={reportData as RequestDiffReport} live={live} running={isRunning} />,
      },
      { key: 'snapshots', label: '请求快照', children: <SnapshotTable runId={id} report={reportData as RequestDiffReport} /> },
      { key: 'diff', label: '差异对比', children: <DiffPanel runId={id} /> },
      { key: 'findings', label: '隐藏指令', children: <FindingsTable runId={id} /> },
      { key: 'items', label: '结果明细', children: <RunItems runId={id} kind={kind} models={models.filter(Boolean)} /> },
      { key: 'config', label: '配置', children: <JsonBlock value={data.config} maxHeight={620} /> },
    ]
  } else if (kind === 'injection') {
    tabs = [
      { key: 'overview', label: '风险总览', children: <InjectionReportView report={reportData as InjectionReport} live={live} /> },
      { key: 'items', label: '结果明细', children: <RunItems runId={id} kind={kind} models={models.filter(Boolean)} /> },
      { key: 'raw', label: '原始报告', children: <JsonBlock value={reportData || {}} maxHeight={620} /> },
      { key: 'config', label: '配置', children: <JsonBlock value={data.config} maxHeight={620} /> },
    ]
  }

  return (
    <>
      <PageHeader
        title={<Space><KindTag kind={kind} /> {kindLabel(kind)} · <span className="small-mono">{id}</span></Space>}
        subtitle={<Space wrap split={<span className="muted">·</span>}><span>创建 {fmtTime(data.run.createdAt)}</span><span>开始 {fmtTime(data.run.startedAt)}</span><span>结束 {fmtTime(data.run.finishedAt)}</span><span>耗时 {fmtDuration(data.run.durationMs || summary?.elapsedMs)}</span></Space>}
        extra={[
          <Button key="back" icon={<ArrowLeftOutlined />} onClick={() => navigate('/runs')}>返回历史</Button>,
          <Button key="refresh" icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>刷新</Button>,
          isRunning && <Popconfirm key="stop" title="停止当前测试？" onConfirm={stop}><Button danger icon={<StopOutlined />} loading={stopping}>停止</Button></Popconfirm>,
          <Dropdown key="export" menu={exportMenu}><Button icon={<DownloadOutlined />}>导出</Button></Dropdown>,
          <Popconfirm key="del" title="删除该测试？" description="结果明细与请求快照会一并删除。" onConfirm={remove} disabled={isRunning}>
            <Button danger icon={<DeleteOutlined />} disabled={isRunning}>删除</Button>
          </Popconfirm>,
        ]}
      />
      {data.run.error && <Alert type="error" showIcon style={{ marginBottom: 14 }} message="测试异常" description={data.run.error} />}
      {data.run.status === 'interrupted' && <Alert type="warning" showIcon style={{ marginBottom: 14 }} message="服务重启导致测试中断，已保留此前结果。" />}
      {headerKpis}
      <div className="panel">
        <Tabs items={tabs as any} destroyInactiveTabPane={false} />
      </div>
    </>
  )
}

function download(url: string) {
  const a = document.createElement('a')
  a.href = url
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}
