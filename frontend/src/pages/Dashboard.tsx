import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Alert, Button, Card, Space, Table, Tag } from 'antd'
import {
  BugOutlined, DiffOutlined, FileSearchOutlined, ThunderboltOutlined, CloudServerOutlined,
  AppstoreOutlined, ExperimentOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import { Kpi, KindTag, PageHeader, StatusTag } from '../components/common'
import { fmtDuration, fmtPct, fmtTime } from '../format'

const quick = [
  { key: '/stress/new', icon: <ThunderboltOutlined />, title: '并发压测', desc: '固定请求、持续时长、并发爬坡；统计 TTFT、TPOT、QPS 与真实并发。', color: '#165dff' },
  { key: '/audit/new', icon: <FileSearchOutlined />, title: 'Token 审计', desc: '多上游输入 Token 对比，结合本地分词器检测异常膨胀与重复波动。', color: '#d97706' },
  { key: '/request-audit/new', icon: <DiffOutlined />, title: '请求差异审计', desc: '完整请求快照、SHA-256 指纹、JSONPath 结构化 Diff 与隐藏指令扫描。', color: '#7c3aed' },
  { key: '/injection/new', icon: <BugOutlined />, title: '提示词注入', desc: '规则 + LLM 裁判混合判定，输出 ASR、分类风险与用例明细。', color: '#dc2626' },
]

export default function Dashboard() {
  const navigate = useNavigate()
  const { data: runs } = useQuery({ queryKey: ['runs', 'recent'], queryFn: () => api.runs({ limit: 8 }) })
  const { data: targets } = useQuery({ queryKey: ['targets'], queryFn: api.targets })
  const { data: prompts } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts })
  const { data: attacks } = useQuery({ queryKey: ['attacks'], queryFn: api.attacks })
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const geminiReady = meta?.tokenizers?.gemini?.some(g => g.downloaded)

  const cols = [
    { title: '类型', dataIndex: 'kind', width: 130, render: (v: string) => <KindTag kind={v} /> },
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
        subtitle="压测、Token 审计、请求差异审计与提示词注入统一在一个独立测试台中运行。"
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
          message="Gemini 本地分词器尚未下载"
          description="可在“设置”中一键下载并校验；下载后 Token 审计与请求审计的本地 Token 对比才可用于 Gemini 模型。"
          action={<Button size="small" onClick={() => navigate('/settings')}>前往设置</Button>}
        />
      )}

      <div className="quick-grid" style={{ marginBottom: 18 }}>
        {quick.map(q => (
          <Card key={q.key} className="quick-card" onClick={() => navigate(q.key)} bodyStyle={{ padding: 18 }}>
            <div style={{ display: 'flex', gap: 12 }}>
              <div style={{ width: 38, height: 38, borderRadius: 10, display: 'grid', placeItems: 'center', background: q.color + '14', color: q.color, fontSize: 18 }}>{q.icon}</div>
              <div>
                <div style={{ fontWeight: 650, fontSize: 15 }}>{q.title}</div>
                <div className="muted" style={{ marginTop: 4, lineHeight: 1.55 }}>{q.desc}</div>
              </div>
            </div>
          </Card>
        ))}
      </div>

      <div className="panel">
        <div className="panel-title">最近测试</div>
        <Table
          rowKey="id"
          size="small"
          dataSource={runs?.items || []}
          columns={cols as any}
          pagination={false}
          onRow={r => ({ onClick: () => navigate(`/runs/${r.id}`), style: { cursor: 'pointer' } })}
          locale={{ emptyText: '还没有测试记录，选择上方任一测试类型开始。' }}
        />
      </div>

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="panel-title">快捷入口</div>
        <Space wrap>
          <Button icon={<CloudServerOutlined />} onClick={() => navigate('/targets')}>管理上游与密钥</Button>
          <Button icon={<AppstoreOutlined />} onClick={() => navigate('/prompts')}>维护提示词库</Button>
          <Button icon={<ExperimentOutlined />} onClick={() => navigate('/attacks')}>维护攻击用例</Button>
        </Space>
      </div>
    </>
  )
}
