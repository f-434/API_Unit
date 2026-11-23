import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Col, Form, InputNumber, Row, Select, Switch, message } from 'antd'
import { PlayCircleOutlined } from '@ant-design/icons'
import { api } from '../api'
import { PageHeader, SummaryLine } from '../components/common'
import { TargetRowsEditor, type TargetRow } from '../components/TargetModel'
import RequestCaseEditor, { newCase, toRequest, type CaseDraft } from '../components/RequestCaseEditor'

export default function RequestDiffNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const [cases, setCases] = useState<CaseDraft[]>([newCase(1)])
  const [submitting, setSubmitting] = useState(false)
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const targets = (Form.useWatch('targets', form) || []) as TargetRow[]
  const reps = Form.useWatch('repetitions', form) || 1
  const validTargets = targets.filter(t => t.targetId && t.model).length
  const planned = useMemo(() => validTargets * cases.length * Number(reps || 0), [validTargets, cases.length, reps])

  const submit = async (v: any) => {
    const rows = (v.targets || []).filter((t: TargetRow) => t.targetId && t.model)
    if (!rows.length) return message.error('请至少配置一个有效的对比目标')
    if (!cases.length) return message.error('请至少创建一个请求用例')
    const ids = new Set<string>()
    const built: any[] = []
    for (const c of cases) {
      if (!c.id.trim()) return message.error('用例 ID 不能为空')
      if (ids.has(c.id)) return message.error(`用例 ID 重复：${c.id}`)
      ids.add(c.id)
      try {
        const req = toRequest(c)
        if (!Array.isArray(req.messages) || !req.messages.length) throw new Error('messages 不能为空')
        if (!req.messages.some((m: any) => m.role === 'user')) throw new Error('至少需要一条 user 消息')
        built.push({ id: c.id.trim(), name: c.name.trim() || c.id, notes: c.notes, request: req })
      } catch (e: any) {
        return message.error(`${c.name || c.id}：${e.message}`)
      }
    }
    setSubmitting(true)
    try {
      const config = {
        targets: rows.map((t: TargetRow) => ({
          targetId: t.targetId, model: t.model, protocol: t.protocol || 'auto',
          tokenizer: t.tokenizer || 'auto', label: t.label || undefined,
        })),
        cases: built,
        repetitions: v.repetitions,
        concurrency: v.concurrency,
        timeoutMs: Number(v.timeoutSec || 120) * 1000,
        maxOutputTokens: v.maxOutputTokens || 0,
        stream: !!v.stream,
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
      const res = await api.createRun('request_diff', config)
      message.success('请求差异审计已启动')
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
      <PageHeader title="新建请求差异审计" />
      <Form
        form={form}
        layout="vertical"
        onFinish={submit}
        initialValues={{
          targets: [{ targetId: '', model: '', label: '', tokenizer: 'auto', protocol: 'auto' }],
          repetitions: 1, concurrency: 4, timeoutSec: 120, maxOutputTokens: 0, stream: false,
          anomalyThresholdPct: 10, baseline: 'min', checkLocal: true,
          maxRetries: 0, backoffMs: 500, maxBackoffMs: 5000, retryOn429: true, retryOn5xx: true, retryOnNetwork: true,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 对比目标</div>
          <Form.Item name="targets" rules={[{ required: true }]}>
            <TargetRowsEditor tokenizers={meta?.tokenizerStrategies || []} showTokenizer />
          </Form.Item>
        </div>

        <div className="panel">
          <div className="panel-title">2. 请求用例</div>
          <RequestCaseEditor value={cases} onChange={setCases} />
        </div>

        <div className="panel">
          <div className="panel-title">3. 执行与判定参数</div>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={3}><Form.Item name="repetitions" label="重复次数"><InputNumber min={1} max={20} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="concurrency" label="并发"><InputNumber min={1} max={32} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="timeoutSec" label="超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="maxOutputTokens" label="输出上限"><InputNumber min={0} max={8192} placeholder="0 = 用例值" style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="anomalyThresholdPct" label="异常阈值 %"><InputNumber min={1} max={100} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}>
              <Form.Item name="baseline" label="Token 基线">
                <Select options={[{ value: 'min', label: '最小中位数' }, { value: 'median', label: '中位数' }]} />
              </Form.Item>
            </Col>
            <Col xs={12} lg={3}><Form.Item name="stream" label="流式" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="checkLocal" label="本地分词器对比" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={3}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={10} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn429" label="429" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn5xx" label="5xx/流中断" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOnNetwork" label="网络" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <SummaryLine items={[
            { label: '目标', value: validTargets },
            { label: '用例', value: cases.length },
            { label: '重复', value: reps },
            { label: '预计请求', value: planned },
          ]} />
          <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={submitting}>启动审计</Button>
        </div>
      </Form>
    </>
  )
}
