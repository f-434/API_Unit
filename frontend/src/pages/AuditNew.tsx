import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, InputNumber, Input, Row, Select, Space, Switch, Tag, message,
} from 'antd'
import { PlayCircleOutlined } from '@ant-design/icons'
import { api } from '../api'
import { PageHeader } from '../components/common'
import { TargetRowsEditor, type TargetRow } from '../components/TargetModel'

export default function AuditNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const { data: prompts } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts })
  const targets = (Form.useWatch('targets', form) || []) as TargetRow[]
  const promptIds = (Form.useWatch('promptIds', form) || []) as string[]
  const reps = Form.useWatch('repetitions', form) || 3

  const planned = useMemo(() => {
    const validTargets = targets.filter(t => t.targetId && t.model).length
    return validTargets * promptIds.length * Number(reps || 0)
  }, [targets, promptIds, reps])

  const submit = async (v: any) => {
    const rows = (v.targets || []).filter((t: TargetRow) => t.targetId && t.model)
    if (!rows.length) return message.error('请至少配置一个有效的对比目标')
    if (!(v.promptIds || []).length) return message.error('请选择至少一个审计提示词')
    setSubmitting(true)
    try {
      const config = {
        targets: rows.map((t: TargetRow) => ({
          targetId: t.targetId, model: t.model, protocol: t.protocol || 'auto',
          tokenizer: t.tokenizer || 'auto', label: t.label || undefined,
        })),
        promptIds: v.promptIds,
        repetitions: v.repetitions,
        maxOutputTokens: v.maxOutputTokens,
        temperature: v.temperature ?? 0,
        stream: !!v.stream,
        concurrency: v.concurrency,
        timeoutMs: Number(v.timeoutSec || 120) * 1000,
        anomalyThresholdPct: v.anomalyThresholdPct,
        baseline: v.baseline,
        checkLocal: !!v.checkLocal,
        retry: {
          maxRetries: v.maxRetries || 0,
          backoffMs: v.backoffMs || 500,
          maxBackoffMs: v.maxBackoffMs || 5000,
          retryOn429: !!v.retryOn429,
          retryOn5xx: !!v.retryOn5xx,
          retryOnNetwork: !!v.retryOnNetwork,
        },
      }
      const res = await api.createRun('token_audit', config)
      message.success('Token 审计已启动')
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
        title="新建 Token 审计"
        subtitle="同一提示词发送到多个上游/模型，对比上报的输入 Token、重复波动与本地分词器偏差，定位中转层额外注入或计费异常。"
      />
      <Form
        form={form}
        layout="vertical"
        onFinish={submit}
        initialValues={{
          targets: [{ targetId: '', model: '', label: '', tokenizer: 'auto', protocol: 'auto' }],
          promptIds: [], repetitions: 3, maxOutputTokens: 8, temperature: 0, stream: false,
          concurrency: 4, timeoutSec: 120, anomalyThresholdPct: 10, baseline: 'min', checkLocal: true,
          maxRetries: 0, backoffMs: 500, maxBackoffMs: 5000, retryOn429: true, retryOn5xx: true, retryOnNetwork: true,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 对比目标</div>
          <Form.Item name="targets" rules={[{ required: true }]}>
            <TargetRowsEditor tokenizers={meta?.tokenizerStrategies || []} showTokenizer />
          </Form.Item>
          <Alert
            type="info"
            showIcon
            message="本地 Token 偏差是判断“注入”的主证据"
            description="OpenAI 模型使用 tiktoken，Gemini 使用 Gemma SentencePiece；跨模型家族（GPT vs Gemini）的 Token 差异是正常现象，应优先看 OverheadPct。Claude 暂用启发式估算，仅作参考。"
          />
        </div>

        <div className="panel">
          <div className="panel-title">2. 审计提示词</div>
          <Form.Item name="promptIds" label="选择提示词（建议覆盖短文本、长文本、中英混合）" rules={[{ required: true, message: '请选择提示词' }]}>
            <Select mode="multiple" allowClear placeholder="从提示词库选择" options={(prompts?.items || []).map(p => ({ value: p.id, label: `${p.name}（${p.content.length} 字）` }))} />
          </Form.Item>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={4}><Form.Item name="repetitions" label="每目标重复次数"><InputNumber min={1} max={20} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxOutputTokens" label="输出上限" extra="越小越省钱，审计只关注输入 Token"><InputNumber min={1} max={4096} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="temperature" label="temperature"><InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="concurrency" label="并发"><InputNumber min={1} max={32} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="timeoutSec" label="超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="stream" label="流式" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
        </div>

        <div className="panel">
          <div className="panel-title">3. 判定参数</div>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={6}>
              <Form.Item name="anomalyThresholdPct" label="异常阈值（%）"><InputNumber min={1} max={100} style={{ width: '100%' }} /></Form.Item>
            </Col>
            <Col xs={12} lg={6}>
              <Form.Item name="baseline" label="基线">
                <Select options={[{ value: 'min', label: '各目标中位数的最小值' }, { value: 'median', label: '各目标中位数的中位数' }]} />
              </Form.Item>
            </Col>
            <Col xs={12} lg={6}><Form.Item name="checkLocal" label="对比本地分词器" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={4}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={10} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn429" label="429" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn5xx" label="5xx" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOnNetwork" label="网络" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <Space wrap>
            <Tag color="blue">目标 {targets.filter(t => t.targetId && t.model).length}</Tag>
            <Tag color="gold">提示词 {promptIds.length}</Tag>
            <Tag color="purple">重复 {reps}</Tag>
            <Tag color="magenta">预计请求 {planned}</Tag>
          </Space>
          <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={submitting}>启动审计</Button>
        </div>
      </Form>
    </>
  )
}
