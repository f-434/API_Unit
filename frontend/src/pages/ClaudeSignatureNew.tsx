import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, Input, InputNumber, Row, Select, Space, Switch, Tag, Tooltip, message,
} from 'antd'
import {
  DeleteOutlined, PlayCircleOutlined, PlusOutlined, SafetyCertificateOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import { PageHeader, SummaryLine } from '../components/common'
import { ModelMultiSelect, TargetSelect, useTargets } from '../components/TargetModel'

interface Row {
  targetId: string
  models: string[]
  authMode: 'auto' | 'api-key' | 'bearer'
  label: string
}

const emptyRow = (): Row => ({ targetId: '', models: [], authMode: 'auto', label: '' })

const THINKING_OPTIONS = [
  { value: 'auto', label: '自动（推荐）' },
  { value: 'adaptive', label: 'adaptive' },
  { value: 'budget', label: 'budget' },
  { value: 'off', label: '关闭' },
]

const EFFORT_OPTIONS = [
  { value: '', label: '不设置' },
  { value: 'low', label: 'low' },
  { value: 'medium', label: 'medium' },
  { value: 'high', label: 'high' },
  { value: 'xhigh', label: 'xhigh' },
  { value: 'max', label: 'max' },
]

export default function ClaudeSignatureNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const { data: targets = [] } = useTargets()
  const [rows, setRows] = useState<Row[]>([emptyRow()])
  const [submitting, setSubmitting] = useState(false)
  const count = Form.useWatch('count', form) || 5
  const concurrency = Form.useWatch('concurrency', form) || 2
  const thinking = Form.useWatch('thinking', form) || 'auto'

  const validRows = useMemo(
    () => rows.filter(r => r.targetId && r.models.length > 0),
    [rows],
  )
  const modelCount = validRows.reduce((n, r) => n + r.models.length, 0)
  const planned = modelCount * Number(count)
  const wrongProtocol = useMemo(() => validRows.filter(r => {
    const t = targets.find(x => x.id === r.targetId)
    return !!t && t.protocol !== 'auto' && t.protocol !== 'claude'
  }), [validRows, targets])

  const setRow = (i: number, patch: Partial<Row>) => {
    setRows(prev => prev.map((r, idx) => idx === i ? { ...r, ...patch } : r))
  }

  const submit = async () => {
    if (!validRows.length) return message.error('请至少选择一个上游和模型')
    const v = form.getFieldsValue()
    const maxTokens = Number(v.maxTokens || 16000)
    if (v.thinking !== 'off' && maxTokens < 1025) {
      return message.error('启用 thinking 时 max_tokens 不能小于 1025')
    }
    setSubmitting(true)
    try {
      const res = await api.createRun('claude_signature', {
        targets: validRows.map(r => ({
          targetId: r.targetId,
          models: r.models,
          authMode: r.authMode,
          label: r.label || undefined,
        })),
        count: Number(v.count || 5),
        concurrency: Number(v.concurrency || 2),
        delayMs: Number(v.delayMs ?? 1000),
        timeoutMs: Number(v.timeoutSec || 180) * 1000,
        maxTokens,
        thinking: v.thinking || 'auto',
        budgetTokens: Number(v.budgetTokens || 4096),
        effort: v.effort || '',
        prompt: v.prompt || '',
        retry: {
          maxRetries: Number(v.maxRetries ?? 2),
          backoffMs: Number(v.backoffMs ?? 800),
          maxBackoffMs: Number(v.maxBackoffMs ?? 5000),
          retryOn429: !!v.retryOn429,
          retryOn5xx: !!v.retryOn5xx,
          retryOnNetwork: !!v.retryOnNetwork,
        },
      })
      message.success('签名核验已启动')
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
      <PageHeader title={<Space><SafetyCertificateOutlined /> 新建 signature 签名核验</Space>} />

      <Form
        form={form}
        layout="vertical"
        initialValues={{
          count: 5,
          concurrency: 2,
          delayMs: 1000,
          timeoutSec: 180,
          maxTokens: 16000,
          thinking: 'auto',
          budgetTokens: 4096,
          effort: '',
          maxRetries: 2,
          backoffMs: 800,
          maxBackoffMs: 5000,
          retryOn429: true,
          retryOn5xx: true,
          retryOnNetwork: true,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 测试目标</div>
          {rows.map((row, i) => {
            const target = targets.find(x => x.id === row.targetId)
            const wrong = !!target && target.protocol !== 'auto' && target.protocol !== 'claude'
            return (
              <div className="case-card" key={i}>
                <Row gutter={[10, 10]} align="middle">
                  <Col xs={24} lg={8}>
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
                      value={row.authMode}
                      onChange={v => setRow(i, { authMode: v })}
                      options={[
                        { value: 'auto', label: '认证 auto' },
                        { value: 'api-key', label: 'x-api-key' },
                        { value: 'bearer', label: 'Bearer' },
                      ]}
                      style={{ width: '100%' }}
                    />
                  </Col>
                  <Col xs={12} lg={3}>
                    <Input value={row.label} onChange={e => setRow(i, { label: e.target.value })} placeholder="标签（可选）" />
                  </Col>
                  <Col flex="auto" style={{ textAlign: 'right' }}>
                    {wrong && <Tag color="orange">非 Claude 协议将被跳过</Tag>}
                    <Button
                      danger
                      type="text"
                      icon={<DeleteOutlined />}
                      disabled={rows.length === 1}
                      onClick={() => setRows(prev => prev.filter((_, idx) => idx !== i))}
                    />
                  </Col>
                </Row>
              </div>
            )
          })}
          <Button type="dashed" block icon={<PlusOutlined />} onClick={() => setRows(prev => [...prev, emptyRow()])}>
            添加上游
          </Button>
          {wrongProtocol.length > 0 && (
            <Alert
              style={{ marginTop: 10 }}
              type="warning"
              showIcon
              message="部分上游不是 Claude Messages 协议"
              description="这些模型启动后会被标记为未执行；请在「上游」中改为 Claude 协议，或使用 claude-* 模型名配合 auto 协议。"
            />
          )}
        </div>

        <div className="panel">
          <div className="panel-title">2. 请求参数</div>
          <Row gutter={[16, 10]}>
            <Col xs={12} lg={4}><Form.Item name="count" label="每模型请求次数"><InputNumber min={1} max={100} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="concurrency" label="模型并发"><InputNumber min={1} max={16} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="delayMs" label="请求间隔 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="timeoutSec" label="单请求超时（秒）"><InputNumber min={5} max={900} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxTokens" label="max_tokens"><InputNumber min={256} max={128000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}>
              <Form.Item name="thinking" label="thinking 模式">
                <Select options={THINKING_OPTIONS} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={[16, 10]}>
            <Col xs={12} lg={4}>
              <Form.Item name="budgetTokens" label="budget_tokens">
                <InputNumber min={1024} max={128000} style={{ width: '100%' }} disabled={thinking === 'adaptive' || thinking === 'off'} />
              </Form.Item>
            </Col>
            <Col xs={12} lg={4}>
              <Form.Item name="effort" label="output_config.effort">
                <Select options={EFFORT_OPTIONS} />
              </Form.Item>
            </Col>
            <Col xs={12} lg={4}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={5} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Row gutter={[16, 10]}>
            <Col xs={8} lg={3}><Form.Item name="retryOn429" label="重试 429" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn5xx" label="重试 5xx" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOnNetwork" label="重试网络错误" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={24} lg={15}>
              <Form.Item name="prompt" label="基础提示词（可选，留空自动生成）">
                <Input.TextArea autoSize={{ minRows: 1, maxRows: 3 }} placeholder="每次请求都会追加随机 nonce，避免 thinking 被抑制" />
              </Form.Item>
            </Col>
          </Row>
          {thinking === 'off' && (
            <Alert
              type="warning"
              showIcon
              message="thinking 已关闭"
              description="关闭 thinking 后模型不会返回签名，本次结果会以“证据不足”为主。"
            />
          )}
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <SummaryLine items={[
            { label: '模型', value: modelCount },
            { label: '预计请求', value: planned },
            { label: '模型并发', value: concurrency },
          ]} />
          <Tooltip title={!modelCount ? '请先选择上游和模型' : ''}>
            <Button
              type="primary"
              size="large"
              icon={<PlayCircleOutlined />}
              loading={submitting}
              disabled={!modelCount}
              onClick={submit}
            >
              启动签名核验
            </Button>
          </Tooltip>
        </div>
      </Form>
    </>
  )
}
