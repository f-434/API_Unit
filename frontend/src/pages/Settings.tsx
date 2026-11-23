import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Descriptions, InputNumber, Popconfirm, Space, Table, Tag, message } from 'antd'
import { CloudDownloadOutlined, DeleteOutlined, ReloadOutlined, SyncOutlined } from '@ant-design/icons'
import { api } from '../api'
import { EllipsisText, PageHeader } from '../components/common'
import AuthenticityHostedSettings from '../components/AuthenticityHostedSettings'
import { fmtDuration } from '../format'

export default function Settings() {
  const qc = useQueryClient()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const { data: meta, refetch: refetchMeta, isFetching: metaFetching } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const [days, setDays] = useState(30)

  const refresh = () => {
    refetchMeta()
    qc.invalidateQueries({ queryKey: ['settings'] })
  }

  const ensureGemini = useMutation({
    mutationFn: api.ensureGeminiTokenizer,
    onSuccess: (r: any) => {
      if (r.ok) message.success('Gemini 本地分词器已就绪')
      else message.error(r.error || '下载失败', 8)
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '下载失败', 8)
      refresh()
    },
  })

  const reloadTokenizers = useMutation({
    mutationFn: api.reloadTokenizers,
    onSuccess: (r: any) => {
      if (r.ok) message.success('本地分词器已重新加载')
      else message.error(r.error || '加载失败', 8)
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '加载失败', 8)
      refresh()
    },
  })

  const resetGemini = useMutation({
    mutationFn: (download: boolean) => api.resetGeminiTokenizer(download),
    onSuccess: (r: any, download: boolean) => {
      if (r.ok) {
        message.success(download ? '已清除并重新下载 Gemini 分词器' : '已清除本地 Gemini 分词器文件')
      } else {
        message.error(r.error || (download ? '重新下载失败' : '清除失败'), 8)
      }
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '操作失败', 8)
      refresh()
    },
  })
  const purge = useMutation({
    mutationFn: () => api.purgeRuns(days),
    onSuccess: (r: any) => { message.success(`已清理 ${r.deleted} 条历史测试`); qc.invalidateQueries({ queryKey: ['runs'] }) },
    onError: (e: any) => message.error(e?.message || '清理失败'),
  })

  const tokenizerRows = meta?.tokenizers?.gemini || []
  const tokenizerErrors = tokenizerRows.filter(r => !!r.error)
  const tokenizerDir = meta?.tokenizers?.tokenizerDir || ''
  const offline = meta?.tokenizers?.offline ?? settings?.offline ?? false

  return (
    <>
      <PageHeader title="设置" />
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
        {offline && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="离线模式：无法自动下载 Gemini 分词器"
            description={<span>请手动将模型文件放入：<span className="small-mono" style={{ wordBreak: 'break-all' }}>{tokenizerDir || '-'}</span></span>}
          />
        )}
        {!!tokenizerErrors.length && (
          <Alert
            type="error"
            showIcon
            style={{ marginBottom: 12 }}
            message="Gemini 本地分词器异常"
            description={<div className="tokenizer-error">{tokenizerErrors.map(r => `${r.name}：${r.error}`).join('\n')}</div>}
            action={(
              <Popconfirm
                title="清除并重新下载 Gemini 分词器？"
                description="将删除本地模型文件并重新下载校验，网络较慢时可能需要几分钟。"
                onConfirm={() => resetGemini.mutate(true)}
              >
                <Button danger size="small" loading={resetGemini.isPending} disabled={offline}>清除并重新下载</Button>
              </Popconfirm>
            )}
          />
        )}
        <Space wrap style={{ marginBottom: 10 }}>
          <Tag color={meta?.tokenizers?.tiktoken ? 'green' : 'default'}>tiktoken：{meta?.tokenizers?.tiktoken ? '已内置' : '不可用'}</Tag>
          <Tag color={offline ? 'orange' : 'blue'}>{offline ? '离线模式' : '在线模式'}</Tag>
          <Button icon={<CloudDownloadOutlined />} loading={ensureGemini.isPending} onClick={() => ensureGemini.mutate()} disabled={offline}>下载 / 校验 Gemini 分词器</Button>
          <Button icon={<SyncOutlined />} loading={reloadTokenizers.isPending} onClick={() => reloadTokenizers.mutate()}>重新加载本地分词器</Button>
          <Popconfirm
            title="清除并重新下载 Gemini 分词器？"
            description="将删除本地模型文件并重新下载校验，网络较慢时可能需要几分钟。"
            onConfirm={() => resetGemini.mutate(true)}
          >
            <Button danger icon={<DeleteOutlined />} loading={resetGemini.isPending} disabled={offline}>清除并重新下载</Button>
          </Popconfirm>
          <Popconfirm title="仅清除本地分词器文件？" onConfirm={() => resetGemini.mutate(false)}>
            <Button size="small" type="text" loading={resetGemini.isPending}>仅清除文件</Button>
          </Popconfirm>
          <Button icon={<ReloadOutlined />} loading={metaFetching} onClick={refresh}>刷新</Button>
        </Space>
        {tokenizerDir && (
          <div className="field-label" style={{ marginBottom: 10, wordBreak: 'break-all' }}>
            分词器目录 <span className="small-mono">{tokenizerDir}</span>
          </div>
        )}
        <Table
          rowKey="name"
          size="small"
          pagination={false}
          dataSource={tokenizerRows}
          tableLayout="fixed"
          scroll={{ x: 860 }}
          columns={[
            { title: 'Tokenizer', dataIndex: 'name', width: 110 },
            { title: '文件', dataIndex: 'file', width: 240, render: (v: string) => <span className="small-mono">{v}</span> },
            { title: '已下载', dataIndex: 'downloaded', width: 80, align: 'center' as const, render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            { title: '已加载', dataIndex: 'loaded', width: 80, align: 'center' as const, render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            {
              title: '错误', dataIndex: 'error', width: 350,
              render: (v: string) => v
                ? <span style={{ color: '#dc2626' }}><EllipsisText text={v} width={330} /></span>
                : <span className="muted">-</span>,
            },
          ]}
        />
      </div>

      <AuthenticityHostedSettings />

      <div className="panel">
        <div className="panel-title">数据清理</div>
        <Space>
          <span>保留最近</span>
          <InputNumber min={1} max={3650} value={days} onChange={v => setDays(Number(v || 30))} />
          <span>天，立即清理更早的数据</span>
          <Button danger icon={<DeleteOutlined />} loading={purge.isPending} onClick={() => purge.mutate()}>执行清理</Button>
        </Space>
      </div>

    </>
  )
}
