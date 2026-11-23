import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, Input, InputNumber, Row, Select, Space,
  Switch, Tag, Tooltip, message,
} from 'antd'
import {
  DeleteOutlined, PlusOutlined, PlayCircleOutlined, SafetyCertificateOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import { PageHeader, SummaryLine } from '../components/common'
import { ModelMultiSelect, TargetSelect, useTargets } from '../components/TargetModel'
import type { AuthenticityHostedConfig } from '../types'

interface Row {
  targetId: string
  models: string[]
  label: string
  protocol: string
  apiStyle: string
  authMode: string
  apiVersion: string
}

const emptyRow = (): Row => ({ targetId: '', models: [], label: '', protocol: 'auto', apiStyle: 'chat', authMode: 'api-key', apiVersion: '' })

const defaultChecks = {
  fingerprint: true,
  source: true,
  capability: false,
  identity: true,
  consistency: true,
  signature: true,
  tokenizer: false,
}

export default function AuthenticityNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const { data: targets = [] } = useTargets()
  const [rows, setRows] = useState<Row[]>([emptyRow()])
  const [checks, setChecks] = useState(defaultChecks)
  const [submitting, setSubmitting] = useState(false)
  const concurrency = Form.useWatch('concurrency', form)
  const repetitions = Form.useWatch('repetitions', form) || 1
  const hostedEnabled = Form.useWatch('hostedEnabled', form)

  const { data: configData } = useQuery({ queryKey: ['authenticityConfig'], queryFn: api.authenticityConfig })
  const hostedSettings = configData?.settings as AuthenticityHostedConfig | undefined
  const hostedReady = !!(hostedSettings?.mcp?.serverUrl || hostedSettings?.proof?.baseUrl || hostedSettings?.proof?.domain)

  useEffect(() => {
    if (!hostedSettings) return
    form.setFieldValue('hostedEnabled', !!hostedSettings.defaultEnabled)
    // When online MCP/proof is the saved default, the local challenge is
    // auxiliary and off by default. Users can still turn it back on.
    if (hostedSettings.defaultEnabled && hostedReady) {
      setChecks(prev => ({ ...prev, capability: false }))
    }
  }, [hostedSettings, hostedReady, form])

  const validRows = useMemo(
    () => rows.filter(r => r.targetId && r.models.length > 0),
    [rows],
  )
  const modelCount = validRows.reduce((n, r) => n + r.models.length, 0)

  const setRow = (i: number, patch: Partial<Row>) => {
    setRows(prev => prev.map((r, idx) => idx === i ? { ...r, ...patch } : r))
  }

  const submit = async () => {
    if (!validRows.length) return message.error('请至少配置一个上游和模型')
    setSubmitting(true)
    try {
      const v = form.getFieldsValue()
      if (v.hostedEnabled && !hostedReady) {
        message.error('已启用工具调用检测，但设置中还没有配置 MCP 或证明页，请先到“设置”完成配置')
        setSubmitting(false)
        return
      }
      const config = {
        targets: validRows.map(r => ({
          targetId: r.targetId,
          models: r.models,
          protocol: r.protocol || 'auto',
          apiStyle: r.apiStyle || 'chat',
          label: r.label || undefined,
          authMode: r.authMode || 'api-key',
          apiVersion: r.apiVersion || undefined,
        })),
        concurrency: v.concurrency,
        repetitions: Number(v.repetitions || 1),
        timeoutMs: Number(v.timeoutSec || 180) * 1000,
        checks,
        hosted: {
          enabled: !!v.hostedEnabled,
          profile: hostedSettings?.profile || 'generic',
          providers: hostedSettings?.providers || { openai: true, claude: true, gemini: true },
          timeoutMs: Number(v.hostedTimeoutSec || 300) * 1000,
        },
        retry: {
          maxRetries: v.maxRetries ?? 1,
          backoffMs: v.backoffMs ?? 800,
          maxBackoffMs: v.maxBackoffMs ?? 5000,
          retryOn429: true,
          retryOn5xx: true,
          retryOnNetwork: true,
        },
      }
      const res = await api.createRun('authenticity', config)
      message.success('模型真实性测试已启动')
      qc.invalidateQueries({ queryKey: ['runs'] })
      navigate(`/runs/${res.id}`)
    } catch (e: any) {
      message.error(e?.message || '启动失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <>
      <PageHeader title={<Space><SafetyCertificateOutlined /> 新建模型真实性测试</Space>} />
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          concurrency: 3, repetitions: 1, timeoutSec: 180,
          hostedEnabled: false, hostedTimeoutSec: 300,
          maxRetries: 1, backoffMs: 800, maxBackoffMs: 5000,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 测试目标</div>
          {rows.map((row, i) => (
            <div className="case-card" key={i}>
              <Row gutter={[10, 10]} align="middle">
                <Col xs={24} lg={4}>
                  <TargetSelect
                    value={row.targetId}
                    targets={targets}
                    placeholder={`目标 ${i + 1}`}
                    onChange={v => {
                      const t = targets.find(x => x.id === v)
                      setRow(i, { targetId: v, models: [], label: row.label || t?.name || '' })
                    }}
                  />
                </Col>
                <Col xs={24} lg={6}>
                  <ModelMultiSelect
                    targetId={row.targetId}
                    value={row.models}
                    onChange={v => setRow(i, { models: v || [] })}
                  />
                </Col>
                <Col xs={12} lg={2}>
                  <Select
                    value={row.protocol}
                    onChange={v => setRow(i, { protocol: v })}
                    options={['auto', 'openai', 'claude', 'gemini'].map(p => ({ value: p, label: p === 'auto' ? '自动' : p }))}
                    style={{ width: '100%' }}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <Tooltip title="来源探针接口（仅 OpenAI 生效；能力挑战仍走 Chat Completions）">
                    <Select
                      value={row.apiStyle}
                      onChange={v => setRow(i, { apiStyle: v })}
                      options={[
                        { value: 'chat', label: 'Chat 接口' },
                        { value: 'responses', label: 'Responses' },
                        { value: 'auto', label: '自动' },
                      ]}
                      disabled={row.protocol === 'claude' || row.protocol === 'gemini'}
                      aria-label="来源探针接口"
                      style={{ width: '100%' }}
                    />
                  </Tooltip>
                </Col>
                <Col xs={12} lg={3}>
                  <Select
                    value={row.authMode}
                    onChange={v => setRow(i, { authMode: v })}
                    options={[{ value: 'api-key', label: 'API Key' }, { value: 'bearer', label: 'Bearer' }]}
                    style={{ width: '100%' }}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <Input value={row.label} onChange={e => setRow(i, { label: e.target.value })} placeholder="标签（可选）" />
                </Col>
                <Col flex="auto" style={{ textAlign: 'right' }}>
                  <Button danger type="text" icon={<DeleteOutlined />} disabled={rows.length === 1} onClick={() => setRows(prev => prev.filter((_, idx) => idx !== i))} />
                </Col>
              </Row>
              {row.targetId && row.models.length === 0 && (
                <div className="muted" style={{ marginTop: 8, fontSize: 12 }}>请选择模型</div>
              )}
            </div>
          ))}
          <Button type="dashed" block icon={<PlusOutlined />} onClick={() => setRows(prev => [...prev, emptyRow()])}>
            添加上游
          </Button>
        </div>

        <div className="panel">
          <div className="panel-title">2. 检测项与并发</div>
          <Row gutter={[16, 10]}>
            <Col xs={12} lg={4}><Form.Item name="concurrency" label="模型并发"><InputNumber min={1} max={16} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="repetitions" label="测试次数"><InputNumber min={1} max={5} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="timeoutSec" label="单请求超时（秒）"><InputNumber min={10} max={900} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={5} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Space wrap>
            {([
              ['fingerprint', '接口指纹'],
              ['source', '响应来源'],
              ['identity', '模型身份特征'],
              ['consistency', '数据一致性'],
              ['signature', '思维签名真实性（Gemini）'],
            ] as const).map(([key, label]) => (
              <Tag.CheckableTag
                key={key}
                checked={checks[key]}
                onChange={checked => setChecks(prev => ({ ...prev, [key]: checked }))}
              >
                {label}
              </Tag.CheckableTag>
            ))}
          </Space>
        </div>

        <div className="panel">
          <div className="panel-title" style={{ justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
            <span>3. 工具调用</span>
            <Button size="small" onClick={() => navigate('/settings')}>去设置</Button>
          </div>
          <Row gutter={[14, 6]} align="middle">
            <Col xs={12} lg={5}>
              <Form.Item name="hostedEnabled" label="本次启用" valuePropName="checked" style={{ marginBottom: 0 }}>
                <Switch />
              </Form.Item>
            </Col>
            <Col xs={12} lg={5}>
              <Form.Item name="hostedTimeoutSec" label="超时（秒）" style={{ marginBottom: 0 }}>
                <InputNumber min={30} max={900} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} lg={14}>
              <Tag color={hostedReady ? 'green' : 'default'}>
                {hostedReady ? '已配置' : '未配置'}
              </Tag>
            </Col>
          </Row>
          {!!hostedEnabled && !hostedReady && (
            <Alert
              style={{ marginTop: 10 }}
              type="error"
              showIcon
              message="已勾选启用但没有可用配置"
              description="请先到设置里保存 MCP 或证明页配置，否则启动会被拒绝。"
            />
          )}
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <SummaryLine items={[
            { label: '模型', value: modelCount },
            { label: '测试', value: `${modelCount * Number(repetitions)} 次` },
            { label: '并发', value: concurrency || 3 },
          ]} />
          <Tooltip title={modelCount ? '' : '请先选择上游和模型'}>
            <Button type="primary" size="large" icon={<PlayCircleOutlined />} loading={submitting} disabled={!modelCount} onClick={submit}>
              启动真实性检测
            </Button>
          </Tooltip>
        </div>
      </Form>
    </>
  )
}

