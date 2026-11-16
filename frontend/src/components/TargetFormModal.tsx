import { useEffect, useState } from 'react'
import { Alert, Button, Checkbox, Form, Input, Modal, Select, Space, Tag, message } from 'antd'
import { ThunderboltOutlined } from '@ant-design/icons'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api'
import type { Target } from '../types'

const NETWORK_ISSUE_RE = /tls handshake timeout|i\/o timeout|connection timed out|connection reset|network is unreachable/i
const hasNetworkIssue = (r: any) => NETWORK_ISSUE_RE.test(JSON.stringify(r || {}))

export default function TargetFormModal({
  open, target, onClose, onSaved,
}: {
  open: boolean
  target?: Target | null
  onClose: () => void
  onSaved?: (target?: Target) => void
}) {
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<any>(null)
  const editing = !!target

  useEffect(() => {
    if (!open) return
    setTestResult(null)
    form.resetFields()
    if (target) {
      form.setFieldsValue({
        name: target.name, baseUrl: target.baseUrl, protocol: target.protocol,
        apiKey: '', clearKey: false,
      })
    } else {
      form.setFieldsValue({ protocol: 'auto' })
    }
  }, [open, target?.id])

  const save = useMutation({
    mutationFn: async (values: any) => {
      const body: any = {
        name: values.name,
        baseUrl: values.baseUrl,
        protocol: values.protocol,
        clearKey: values.clearKey,
      }
      if (values.apiKey) body.apiKey = values.apiKey
      if (target) return api.updateTarget(target.id, body)
      return api.createTarget(body)
    },
    onSuccess: (res: any) => {
      message.success(editing ? '已更新上游' : '已添加上游')
      qc.invalidateQueries({ queryKey: ['targets'] })
      onSaved?.(res?.item)
      onClose()
    },
    onError: (e: any) => message.error(e?.message || '保存失败'),
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

  return (
    <Modal
      open={open}
      title={editing ? '编辑上游' : '添加上游'}
      onCancel={onClose}
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
  )
}
