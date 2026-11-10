import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Descriptions, InputNumber, Space, Table, Tag, message } from 'antd'
import { CloudDownloadOutlined, DeleteOutlined, ReloadOutlined } from '@ant-design/icons'
import { api } from '../api'
import { PageHeader } from '../components/common'
import { fmtDuration } from '../format'

export default function Settings() {
  const qc = useQueryClient()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const [days, setDays] = useState(30)

  const ensureGemini = useMutation({
    mutationFn: api.ensureGeminiTokenizer,
    onSuccess: (r: any) => {
      if (r.ok) message.success('Gemini 本地分词器已就绪')
      else message.error(r.error || '下载失败')
      qc.invalidateQueries({ queryKey: ['meta'] })
      qc.invalidateQueries({ queryKey: ['settings'] })
    },
    onError: (e: any) => message.error(e?.message || '下载失败'),
  })
  const purge = useMutation({
    mutationFn: () => api.purgeRuns(days),
    onSuccess: (r: any) => { message.success(`已清理 ${r.deleted} 条历史测试`); qc.invalidateQueries({ queryKey: ['runs'] }) },
    onError: (e: any) => message.error(e?.message || '清理失败'),
  })

  const tokenizerRows = meta?.tokenizers?.gemini || []

  return (
    <>
      <PageHeader title="设置与运行信息" subtitle="系统完全独立运行，只通过 HTTP 访问 New API / OpenAI 兼容接口，不修改或侵入 New API 源码。" />
      <div className="panel">
        <div className="panel-title">运行信息</div>
        <Descriptions size="small" column={2} bordered>
          <Descriptions.Item label="版本">{settings?.version || '-'}</Descriptions.Item>
          <Descriptions.Item label="运行时长">{fmtDuration((settings?.uptimeSec || 0) * 1000)}</Descriptions.Item>
          <Descriptions.Item label="数据目录"><span className="small-mono">{settings?.dataDir || '-'}</span></Descriptions.Item>
          <Descriptions.Item label="前端目录"><span className="small-mono">{settings?.webDir || '-'}</span></Descriptions.Item>
          <Descriptions.Item label="访问密码">{settings?.authEnabled ? <Tag color="green">已启用</Tag> : <Tag>未启用</Tag>}</Descriptions.Item>
          <Descriptions.Item label="离线模式">{settings?.offline ? <Tag color="orange">是（禁止下载分词器）</Tag> : <Tag>否</Tag>}</Descriptions.Item>
          <Descriptions.Item label="保留天数">{settings?.retentionDays ?? '-'} 天</Descriptions.Item>
          <Descriptions.Item label="当前运行中的测试">{settings?.activeRuns ?? 0} / {settings?.maxConcurrentRuns ?? '-'}</Descriptions.Item>
        </Descriptions>
      </div>

      <div className="panel">
        <div className="panel-title">本地分词器</div>
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="Token 预估与审计口径"
          description="OpenAI 模型使用内置 tiktoken（o200k_base / cl100k_base，离线可用）；Gemini 使用官方 Gemma SentencePiece 模型（首次下载后离线可用）；Claude 暂不提供本地分词器，使用 usage 或启发式估算。实际计费仍以上游返回的 usage 为准。"
        />
        <Space style={{ marginBottom: 12 }}>
          <Tag color={meta?.tokenizers?.tiktoken ? 'green' : 'default'}>tiktoken：{meta?.tokenizers?.tiktoken ? '已内置' : '不可用'}</Tag>
          <Button icon={<CloudDownloadOutlined />} loading={ensureGemini.isPending} onClick={() => ensureGemini.mutate()}>下载 / 校验 Gemini 分词器</Button>
          <Button icon={<ReloadOutlined />} onClick={() => { qc.invalidateQueries({ queryKey: ['meta'] }); qc.invalidateQueries({ queryKey: ['settings'] }) }}>刷新</Button>
        </Space>
        <Table
          rowKey="name"
          size="small"
          pagination={false}
          dataSource={tokenizerRows}
          columns={[
            { title: 'Tokenizer', dataIndex: 'name' },
            { title: '文件', dataIndex: 'file', render: (v: string) => <span className="small-mono">{v}</span> },
            { title: '已下载', dataIndex: 'downloaded', render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            { title: '已加载', dataIndex: 'loaded', render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            { title: '错误', dataIndex: 'error', render: (v: string) => v ? <span style={{ color: '#dc2626' }}>{v}</span> : <span className="muted">-</span> },
          ]}
        />
      </div>

      <div className="panel">
        <div className="panel-title">数据清理</div>
        <p className="muted">按完成时间删除旧测试记录（包含结果明细与请求快照）。运行中的测试不会被删除。</p>
        <Space>
          <span>保留最近</span>
          <InputNumber min={1} max={3650} value={days} onChange={v => setDays(Number(v || 30))} />
          <span>天，立即清理更早的数据</span>
          <Button danger icon={<DeleteOutlined />} loading={purge.isPending} onClick={() => purge.mutate()}>执行清理</Button>
        </Space>
      </div>

      <div className="panel">
        <div className="panel-title">安全与边界</div>
        <ul className="muted" style={{ margin: 0, paddingLeft: 18 }}>
          <li>API Key 使用本机随机密钥进行 AES-256-GCM 加密存储，接口仅返回掩码；不会写入 localStorage。</li>
          <li>请求快照记录的是本工具实际发出的请求正文与 SHA-256；若中转层在服务端改写请求，需要结合 New API 日志核对。</li>
          <li>Gemini 分词器文件首次运行时会从 Google 官方仓库下载并校验 SHA-256；离线环境可预先把模型文件放入分词器目录。</li>
          <li>默认只监听 127.0.0.1；如需暴露到网络，请设置 AUTH_PASSWORD 并置于可信反向代理之后。</li>
        </ul>
      </div>
    </>
  )
}
