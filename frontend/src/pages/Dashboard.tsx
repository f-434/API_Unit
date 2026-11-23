import ScrollTable from '../components/ScrollTable'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Alert, Button, Tag } from 'antd'
import {
  BugOutlined, DiffOutlined, FileSearchOutlined, ThunderboltOutlined,
  SafetyCertificateOutlined, PictureOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import { Kpi, KindTag, PageHeader, StatusTag, TargetTags } from '../components/common'
import { fmtDuration, fmtPct, fmtTime } from '../format'

const quick = [
  { key: '/stress/new', icon: <ThunderboltOutlined />, title: '并发压测', color: '#165dff' },
  { key: '/audit/new', icon: <FileSearchOutlined />, title: 'Token 审计', color: '#d97706' },
  { key: '/request-audit/new', icon: <DiffOutlined />, title: '请求差异审计', color: '#7c3aed' },
  { key: '/injection/new', icon: <BugOutlined />, title: '提示词注入', color: '#dc2626' },
  { key: '/authenticity/new', icon: <SafetyCertificateOutlined />, title: '模型真实性', color: '#0891b2' },
  { key: '/image-detection/new', icon: <PictureOutlined />, title: '生图检测', color: '#c026d3' },
]

export default function Dashboard() {
  const navigate = useNavigate()
  const { data: runs } = useQuery({
    queryKey: ['runs', 'recent'],
    queryFn: () => api.runs({ limit: 8 }),
    refetchOnMount: 'always',
    refetchInterval: (query) => {
      const items = (query.state.data as any)?.items as any[] | undefined
      return items?.some(r => r.status === 'running' || r.status === 'queued') ? 4000 : false
    },
  })
  const { data: targets } = useQuery({ queryKey: ['targets'], queryFn: api.targets })
  const { data: prompts } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts })
  const { data: attacks } = useQuery({ queryKey: ['attacks'], queryFn: api.attacks })
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const geminiReady = meta?.tokenizers?.gemini?.some(g => g.downloaded)

  const cols = [
    { title: '类型', dataIndex: 'kind', width: 130, render: (v: string) => <KindTag kind={v} /> },
    { title: '上游', dataIndex: ['summary', 'targets'], width: 220, render: (v: any) => <TargetTags targets={v} /> },
    { title: '状态', dataIndex: 'status', width: 100, render: (v: string) => <StatusTag status={v} /> },
    { title: '模型', dataIndex: ['summary', 'models'], render: (v: string[]) => (v || []).slice(0, 3).map(m => <Tag key={m}>{m}</Tag>) },
    { title: '请求', width: 90, render: (_: any, r: any) => <span>{r.summary?.total ?? 0}</span> },
    { title: '成功率', width: 100, render: (_: any, r: any) => <span>{fmtPct(r.summary?.successRate)}</span> },
    { title: 'QPS', width: 90, render: (_: any, r: any) => <span>{(r.summary?.qps ?? 0).toFixed(2)}</span> },
    { title: 'RPM', width: 90, render: (_: any, r: any) => <span>{r.kind === 'stress' ? (r.summary?.rpm ?? 0).toFixed(2) : '-'}</span> },
    { title: '耗时', width: 100, render: (_: any, r: any) => <span>{fmtDuration(r.summary?.elapsedMs || r.durationMs)}</span> },
    { title: '时间', dataIndex: 'createdAt', width: 170, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
  ]

  return (
    <>
      <PageHeader
        title="概览"
        extra={<Button onClick={() => navigate('/runs')}>查看全部历史</Button>}
      />
      <div className="kpi-grid" style={{ marginBottom: 16 }}>
        <Kpi label="上游目标" value={targets?.items.length ?? '-'} />
        <Kpi label="提示词" value={prompts?.items.length ?? '-'} />
        <Kpi label="攻击用例" value={attacks?.items.length ?? '-'} />
        <Kpi label="测试并发上限" value={meta ? meta.limits.maxConcurrentRuns : '-'} />
        <Kpi label="Gemini 本地分词器" value={geminiReady ? '已就绪' : '未下载'} color={geminiReady ? '#16a34a' : '#d97706'} />
      </div>

      {!geminiReady && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="Gemini 本地分词器未下载"
          action={<Button size="small" onClick={() => navigate('/settings')}>前往设置</Button>}
        />
      )}

      <div className="quick-grid" style={{ marginBottom: 18 }}>
        {quick.map(q => (
          <button key={q.key} type="button" className="quick-card" onClick={() => navigate(q.key)}>
            <span className="quick-icon" style={{ background: q.color + '14', color: q.color }}>{q.icon}</span>
            <span className="quick-title">{q.title}</span>
          </button>
        ))}
      </div>

      <div className="panel">
        <div className="panel-title">最近测试</div>
        <ScrollTable
          rowKey="id"
          size="small"
          dataSource={runs?.items || []}
          columns={cols as any}
          scroll={{ x: 1560 }}
          pagination={false}
          onRow={r => ({ onClick: () => navigate(`/runs/${r.id}`), style: { cursor: 'pointer' } })}
          locale={{ emptyText: '暂无测试记录' }}
        />
      </div>
    </>
  )
}
