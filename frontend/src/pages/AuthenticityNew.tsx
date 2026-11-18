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
import { PageHeader } from '../components/common'
import { ModelMultiSelect, TargetSelect, useTargets } from '../components/TargetModel'
import type { AuthenticityHostedConfig } from '../types'

interface Row {
  targetId: string
  models: string[]
  label: string
  protocol: string
  authMode: string
  apiVersion: string
}

const emptyRow = (): Row => ({ targetId: '', models: [], label: '', protocol: 'auto', authMode: 'api-key', apiVersion: '' })

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
      <PageHeader
        title={<Space><SafetyCertificateOutlined /> 新建模型真实性测试</Space>}
        subtitle="默认使用「设置」中统一保存的工具调用检测（MCP / 网页搜索）；多个模型可按并发和测试次数执行，结果统一进入测试详情。"
      />
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
          <div className="panel-title">1. 测试目标（可勾选多个模型）</div>
          {rows.map((row, i) => (
            <div className="case-card" key={i}>
              <Row gutter={[10, 10]} align="middle">
                <Col xs={24} lg={5}>
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
                <Col xs={24} lg={8}>
                  <ModelMultiSelect
                    targetId={row.targetId}
                    value={row.models}
                    onChange={v => setRow(i, { models: v || [] })}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <Select
                    value={row.protocol}
                    onChange={v => setRow(i, { protocol: v })}
                    options={['auto', 'openai', 'claude', 'gemini'].map(p => ({ value: p, label: p === 'auto' ? '自动' : p }))}
                    style={{ width: '100%' }}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <Select
                    value={row.authMode}
                    onChange={v => setRow(i, { authMode: v })}
                    options={[{ value: 'api-key', label: 'API Key' }, { value: 'bearer', label: 'Bearer' }]}
                    style={{ width: '100%' }}
                  />
                </Col>
                <Col xs={16} lg={4}>
                  <Input value={row.label} onChange={e => setRow(i, { label: e.target.value })} placeholder="标签（可选）" />
                </Col>
                <Col flex="auto" style={{ textAlign: 'right' }}>
                  <Button danger type="text" icon={<DeleteOutlined />} disabled={rows.length === 1} onClick={() => setRows(prev => prev.filter((_, idx) => idx !== i))} />
                </Col>
              </Row>
              {row.targetId && row.models.length === 0 && (
                <div className="muted" style={{ marginTop: 8, fontSize: 12 }}>请选择至少一个模型；模型列表可点击下拉框右侧刷新。</div>
              )}
            </div>
          ))}
          <Button type="dashed" block icon={<PlusOutlined />} onClick={() => setRows(prev => [...prev, emptyRow()])}>
            添加上游
          </Button>
          <div className="muted" style={{ marginTop: 10, fontSize: 12 }}>
            每个模型每轮运行：接口指纹 + 响应来源 + 模型身份 + 数据一致性 + 工具调用检测；“测试次数”表示每个模型完整重复几轮。
            当前已选 <b>{modelCount}</b> 个模型 × <b>{repetitions}</b> 次 = <b>{modelCount * Number(repetitions)}</b> 次测试，按并发数同时调度。
          </div>
        </div>

        <div className="panel">
          <div className="panel-title">2. 检测项与并发</div>
          <Row gutter={[16, 10]}>
            <Col xs={12} lg={4}><Form.Item name="concurrency" label="模型并发"><InputNumber min={1} max={16} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="repetitions" label="测试次数" extra="每个模型完整跑几轮"><InputNumber min={1} max={5} style={{ width: '100%' }} /></Form.Item></Col>
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
          <Alert
            style={{ marginTop: 12 }}
            type="info"
            showIcon
            message="N/A 不算失败"
            description="目标/模型不支持某个探针时记为 N/A，不参与通过率，也不判为仿冒。分词器对比默认关闭，开启后也只作为参考证据。"
          />
        </div>

        <div className="panel">
          <div className="panel-title" style={{ justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
            <span>3. 工具调用情况（统一配置）</span>
            <Button size="small" onClick={() => navigate('/settings')}>去设置修改</Button>
          </div>
          <Row gutter={[14, 6]} align="middle">
            <Col xs={12} lg={5}>
              <Form.Item name="hostedEnabled" label="本次启用工具调用检测" valuePropName="checked" style={{ marginBottom: 0 }}>
                <Switch />
              </Form.Item>
            </Col>
            <Col xs={12} lg={5}>
              <Form.Item name="hostedTimeoutSec" label="工具调用超时（秒）" style={{ marginBottom: 0 }}>
                <InputNumber min={30} max={900} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} lg={14}>
              <Space wrap>
                <Tag color={hostedReady ? 'green' : 'default'}>
                  {hostedReady ? '已配置' : '未配置'}
                </Tag>
                <Tag>Profile: {hostedSettings?.profile || 'generic'}</Tag>
                <Tag>MCP: {hostedSettings?.mcp?.serverUrl || '-'}</Tag>
                <Tag>证明域名: {hostedSettings?.proof?.domain || hostedSettings?.legacy?.domain || '-'}</Tag>
                <span className="muted" style={{ fontSize: 12 }}>
                  保存到设置后，新建测试默认按此配置启用，无需每次重填。
                </span>
              </Space>
            </Col>
          </Row>
          {!hostedReady && (
            <Alert
              style={{ marginTop: 10 }}
              type="warning"
              showIcon
              message="工具调用检测尚未配置"
              description="到「设置 → 模型真实性 / 工具调用」填写 MCP Server URL、headers 与证明域名；配置保存后这里会默认启用。未配置时保持关闭即可，本地随机挑战仍会执行。"
            />
          )}
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
          <Space wrap>
            <Tag color="cyan">模型 {modelCount}</Tag>
            <Tag color="purple">测试 {modelCount * Number(repetitions)} 次</Tag>
            <Tag color="blue">并发 {concurrency || 3}</Tag>
            <Tag color="gold">工具调用 {hostedEnabled ? '启用' : '关闭'}</Tag>
          </Space>
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

