import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Checkbox, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag, Tooltip, message,
} from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Target } from '../types'
import { PageHeader, ProtocolTag, CopyButton } from '../components/common'
import { fmtTime } from '../format'

const NETWORK_ISSUE_RE = /tls handshake timeout|i\/o timeout|connection timed out|connection reset|network is unreachable/i
const hasNetworkIssue = (r: any) => NETWORK_ISSUE_RE.test(JSON.stringify(r || {}))

export default function Targets() {
  const qc = useQueryClient()
  const { data: targets = [], isLoading } = useQuery({ queryKey: ['targets'], queryFn: api.targets, select: d => d.items })
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Target | null>(null)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<any>(null)
  const [form] = Form.useForm()

  const openCreate = () => {
    setEditing(null)
    setTestResult(null)
    form.resetFields()
    form.setFieldsValue({ protocol: 'auto' })
    setOpen(true)
  }
  const openEdit = (t: Target) => {
    setEditing(t)
    setTestResult(null)
    form.setFieldsValue({ name: t.name, baseUrl: t.baseUrl, protocol: t.protocol, apiKey: '', clearKey: false })
    setOpen(true)
  }

  const save = useMutation({
    mutationFn: async (values: any) => {
      const body: any = {
        name: values.name,
        baseUrl: values.baseUrl,
        protocol: values.protocol,
        clearKey: values.clearKey,
      }
      if (values.apiKey) body.apiKey = values.apiKey
      if (editing) return api.updateTarget(editing.id, body)
      return api.createTarget(body)
    },
    onSuccess: () => {
      message.success(editing ? '已更新上游' : '已添加上游')
      setOpen(false)
      qc.invalidateQueries({ queryKey: ['targets'] })
    },
    onError: (e: any) => message.error(e?.message || '保存失败'),
  })

  const del = useMutation({
    mutationFn: (id: string) => api.deleteTarget(id),
    onSuccess: () => {
      message.success('已删除')
      qc.invalidateQueries({ queryKey: ['targets'] })
    },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })

  const fetchModels = useMutation({
    mutationFn: (id: string) => api.targetModels(id),
    onSuccess: (res: any) => {
      message.success(`已获取 ${res.models?.length || 0} 个模型`)
      qc.invalidateQueries({ queryKey: ['targets'] })
      qc.invalidateQueries({ queryKey: ['models'] })
    },
    onError: (e: any) => message.error(e?.message || '获取模型失败', 8),
  })

  const testConnection = async () => {
    const v = form.getFieldsValue()
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testTarget({ baseUrl: v.baseUrl, apiKey: v.apiKey || undefined, protocol: v.protocol })
      setTestResult(res)
      if (res.ok) message.success(`连接成功，发现 ${res.models?.length || 0} 个模型`)
      else message.error(res.error || '连接失败')
    } catch (e: any) {
      setTestResult({ ok: false, error: e?.message })
    } finally {
      setTesting(false)
    }
  }

  const columns = [
    {
      title: '名称', dataIndex: 'name', width: 180,
      render: (v: string) => <b>{v}</b>,
    },
    {
      title: 'Base URL', dataIndex: 'baseUrl',
      render: (v: string) => <span className="small-mono">{v}</span>,
    },
    {
      title: '协议', dataIndex: 'protocol', width: 110,
      render: (v: string) => <ProtocolTag protocol={v} />,
    },
    {
      title: 'API Key', dataIndex: 'keyMasked', width: 170,
      render: (v: string) => v ? <Tag color="green">{v}</Tag> : <Tag>未配置</Tag>,
    },
    {
      title: '模型数', dataIndex: 'modelCount', width: 90,
      render: (v: number) => v ? <Tag color="blue">{v}</Tag> : <span className="muted">-</span>,
    },
    {
      title: '最近检测', dataIndex: 'lastTestedAt', width: 170,
      render: (v: number) => <span className="muted">{fmtTime(v)}</span>,
    },
    {
      title: '操作', key: 'actions', width: 220,
      render: (_: any, r: Target) => (
        <Space size={4}>
          <Button size="small" icon={<ReloadOutlined />} loading={fetchModels.isPending} onClick={() => fetchModels.mutate(r.id)}>取模型</Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          <Popconfirm title="删除该上游？" description="不会删除历史测试记录。" onConfirm={() => del.mutate(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="上游 / 供应商"
        subtitle="保存 New API 或其他 OpenAI 兼容网关的地址与密钥；密钥使用本机 AES-256-GCM 加密存储，接口只返回掩码。"
        extra={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>添加上游</Button>}
      />
      <div className="panel">
        <Table
          rowKey="id"
          loading={isLoading}
          dataSource={targets}
          columns={columns as any}
          pagination={false}
          locale={{ emptyText: '还没有上游，点击右上角添加。' }}
        />
      </div>

      <Modal
        open={open}
        title={editing ? '编辑上游' : '添加上游'}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
        width={640}
        destroyOnClose
      >
        <Form form={form} layout="vertical" onFinish={v => save.mutate(v)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="例如：生产 New API" />
          </Form.Item>
          <Form.Item
            name="baseUrl"
            label="Base URL"
            rules={[{ required: true, message: '请输入 Base URL' }]}
            extra="支持带或不带 /v1、/v1beta、/v1/chat/completions 后缀，保存时会自动规范化。"
          >
            <Input placeholder="https://api.example.com 或 http://127.0.0.1:3000" />
          </Form.Item>
          <Form.Item name="protocol" label="协议">
            <Select options={[
              { value: 'auto', label: '自动（按模型名推断）' },
              { value: 'openai', label: 'OpenAI Chat Completions（推荐 New API）' },
              { value: 'claude', label: 'Anthropic Messages（原生）' },
              { value: 'gemini', label: 'Gemini generateContent（原生）' },
            ]} />
          </Form.Item>
          <Form.Item
            name="apiKey"
            label={editing ? 'API Key（留空保持原值）' : 'API Key'}
            rules={editing ? [] : [{ required: true, message: '请输入 API Key' }]}
            extra="不会被写入浏览器 localStorage，也不会出现在测试历史配置中。"
          >
            <Input.Password placeholder="sk-..." autoComplete="new-password" />
          </Form.Item>
          {editing && (
            <Form.Item name="clearKey" valuePropName="checked">
              <Checkbox>清除已保存的 API Key</Checkbox>
            </Form.Item>
          )}
          <Space>
            <Button icon={<ThunderboltOutlined />} loading={testing} onClick={testConnection}>测试连接并获取模型</Button>
          </Space>
          {testResult && (
            <Alert
              style={{ marginTop: 14 }}
              type={testResult.ok ? 'success' : 'error'}
              showIcon
              message={testResult.ok ? `连接成功 · ${testResult.models?.length || 0} 个模型 · ${testResult.elapsedMs || 0} ms` : `连接失败：${testResult.error || '未知错误'}`}
              description={
                <div>
                  {testResult.endpoint && <div className="muted">端点：{testResult.endpoint}</div>}
                  {!!testResult.warnings?.length && <div className="muted">尝试提示：{testResult.warnings.join('；')}</div>}
                  {!testResult.ok && hasNetworkIssue(testResult) && (
                    <div style={{ marginTop: 8 }}>
                      <b>网络诊断：</b>到该地址的 TLS 握手超时。建议更换网络或开启代理，并在启动服务前设置 <code>HTTPS_PROXY</code> / <code>HTTP_PROXY</code>；也可调大 <code>HTTP_TLS_TIMEOUT_SEC</code>、<code>DISCOVER_TIMEOUT_SEC</code>、<code>DISCOVER_TOTAL_TIMEOUT_SEC</code>、<code>DISCOVER_RETRIES</code>。
                    </div>
                  )}
                  {!!testResult.models?.length && (
                    <div style={{ marginTop: 6, maxHeight: 120, overflow: 'auto' }}>
                      {testResult.models.slice(0, 40).map((m: any) => <Tag key={m.id}>{m.id}</Tag>)}
                      {testResult.models.length > 40 && <span className="muted">等 {testResult.models.length} 个</span>}
                    </div>
                  )}
                </div>
              }
            />
          )}
        </Form>
      </Modal>
    </>
  )
}
