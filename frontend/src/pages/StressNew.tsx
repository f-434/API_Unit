import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, Input, InputNumber, Row, Segmented, Select, Space, Switch, Tag, message,
} from 'antd'
import { PlayCircleOutlined } from '@ant-design/icons'
import { api } from '../api'
import { PageHeader } from '../components/common'
import { ModelMultiSelect, TargetSelect, useTargets } from '../components/TargetModel'

export default function StressNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const { data: targets = [] } = useTargets()
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const { data: prompts } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts })
  const targetId = Form.useWatch('targetId', form)
  const mode = Form.useWatch('mode', form) || 'fixed'
  const models = (Form.useWatch('models', form) || []) as string[]
  const total = Form.useWatch('totalRequests', form) || 0
  const concurrency = Form.useWatch('concurrency', form) || 0
  const durationSec = Form.useWatch('durationSec', form) || 0

  const planned = useMemo(() => mode === 'fixed' ? models.length * Number(total || 0) : 0, [mode, models, total])

  const submit = async (v: any) => {
    setSubmitting(true)
    try {
      const config = {
        targetId: v.targetId,
        models: (v.models || []).map((m: string) => ({ model: m, protocol: 'auto' })),
        promptIds: v.promptIds || [],
        request: {
          systemPrompt: v.systemPrompt || '',
          prompt: v.prompt,
          maxTokens: v.maxTokens,
          temperature: v.temperature ?? null,
          topP: v.topP ?? null,
          stream: !!v.stream,
        },
        load: {
          mode: v.mode,
          totalRequests: v.totalRequests,
          concurrency: v.concurrency,
          durationSec: v.durationSec,
          rampStart: v.rampStart,
          rampEnd: v.rampEnd,
          rampStep: v.rampStep,
          rampStepSec: v.rampStepSec,
          rpsLimit: v.rpsLimit || 0,
        },
        retry: {
          maxRetries: v.maxRetries || 0,
          backoffMs: v.backoffMs || 500,
          maxBackoffMs: v.maxBackoffMs || 5000,
          retryOn429: !!v.retryOn429,
          retryOn5xx: !!v.retryOn5xx,
          retryOnNetwork: !!v.retryOnNetwork,
        },
        timeoutMs: Number(v.timeoutSec || 120) * 1000,
        parallelModels: !!v.parallelModels,
        captureBodies: !!v.captureBodies,
        tokenizerStrategy: v.tokenizerStrategy || 'auto',
      }
      const res = await api.createRun('stress', config)
      message.success('压测已启动')
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
        title="新建并发压测"
        subtitle="闭环保并发调度，支持固定请求数、持续时长与并发爬坡。同一进程内多模型可串行或并行执行。"
      />
      <Form
        form={form}
        layout="vertical"
        onFinish={submit}
        initialValues={{
          models: [], promptIds: [], systemPrompt: '', prompt: '请用三句话介绍你自己。',
          maxTokens: 256, temperature: 0.7, topP: null, stream: true, timeoutSec: 120,
          mode: 'fixed', totalRequests: 20, concurrency: 5, durationSec: 60,
          rampStart: 1, rampEnd: 10, rampStep: 2, rampStepSec: 10, rpsLimit: 0,
          maxRetries: 0, backoffMs: 500, maxBackoffMs: 5000, retryOn429: true, retryOn5xx: true, retryOnNetwork: true,
          parallelModels: false, captureBodies: false, tokenizerStrategy: 'auto',
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 目标与模型</div>
          <Row gutter={[14, 6]}>
            <Col xs={24} lg={10}>
              <Form.Item name="targetId" label="上游目标" rules={[{ required: true, message: '请选择上游' }]}>
                <TargetSelect targets={targets} />
              </Form.Item>
            </Col>
            <Col xs={24} lg={14}>
              <Form.Item name="models" label="模型（可多选/手输）" rules={[{ required: true, message: '至少选择一个模型' }]}>
                <ModelMultiSelect targetId={targetId} />
              </Form.Item>
            </Col>
          </Row>
          {!targets.length && <Alert type="warning" showIcon message="还没有上游，请先到“上游 / 供应商”添加 Base URL 与 API Key。" />}
        </div>

        <div className="panel">
          <div className="panel-title">2. 请求参数</div>
          <Form.Item name="promptIds" label="提示词轮换（可选）" extra="选择后会按请求序号轮换这些提示词；未选择时使用下方固定提示词。">
            <Select mode="multiple" allowClear placeholder="选择提示词库中的提示词" options={(prompts?.items || []).map(p => ({ value: p.id, label: p.name }))} />
          </Form.Item>
          <Row gutter={[14, 6]}>
            <Col xs={24} lg={12}>
              <Form.Item name="systemPrompt" label="System Prompt"><Input.TextArea rows={4} placeholder="可留空" /></Form.Item>
            </Col>
            <Col xs={24} lg={12}>
              <Form.Item name="prompt" label="User Prompt" rules={[{ required: true, message: '请输入提示词' }]}>
                <Input.TextArea rows={4} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={4}><Form.Item name="maxTokens" label="maxTokens"><InputNumber min={1} max={32000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="temperature" label="temperature"><InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="topP" label="top_p（可空）"><InputNumber min={0} max={1} step={0.05} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="timeoutSec" label="单请求超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="stream" label="流式" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={12} lg={5}>
              <Form.Item name="tokenizerStrategy" label="缺失 usage 时的估算">
                <Select options={(meta?.tokenizerStrategies || []).map(s => ({ value: s.id, label: s.label }))} />
              </Form.Item>
            </Col>
          </Row>
        </div>

        <div className="panel">
          <div className="panel-title">3. 负载模型</div>
          <Form.Item name="mode">
            <Segmented block options={[
              { label: '固定请求数', value: 'fixed' },
              { label: '持续时长', value: 'duration' },
              { label: '并发爬坡', value: 'ramp' },
            ]} />
          </Form.Item>
          {mode === 'fixed' && (
            <Row gutter={[14, 6]}>
              <Col xs={12} lg={6}><Form.Item name="totalRequests" label="每模型请求数"><InputNumber min={1} max={200000} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={12} lg={6}><Form.Item name="concurrency" label="并发"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
          )}
          {mode === 'duration' && (
            <Row gutter={[14, 6]}>
              <Col xs={12} lg={6}><Form.Item name="durationSec" label="持续秒数"><InputNumber min={5} max={3600} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={12} lg={6}><Form.Item name="concurrency" label="并发"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
          )}
          {mode === 'ramp' && (
            <Row gutter={[14, 6]}>
              <Col xs={12} lg={4}><Form.Item name="rampStart" label="起始并发"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={12} lg={4}><Form.Item name="rampEnd" label="结束并发"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={12} lg={4}><Form.Item name="rampStep" label="步长"><InputNumber min={1} max={500} style={{ width: '100%' }} /></Form.Item></Col>
              <Col xs={12} lg={4}><Form.Item name="rampStepSec" label="每阶段秒数"><InputNumber min={3} max={600} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
          )}
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={6}>
              <Form.Item name="rpsLimit" label="RPS 上限（0 = 不限）" extra="在并发之外额外限速，适合保护上游。">
                <InputNumber min={0} step={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={12} lg={6}><Form.Item name="parallelModels" label="多模型并行" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={12} lg={6}>
              <Form.Item name="captureBodies" label="保存请求/响应正文" valuePropName="checked" extra="开启后明细与导出报告可查看正文；上限：请求 8MB / 响应 1MB / 输出 256KB。">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
        </div>

        <div className="panel">
          <div className="panel-title">4. 重试策略（默认关闭）</div>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={4}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={10} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn429" label="429" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOn5xx" label="5xx/流中断" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={3}><Form.Item name="retryOnNetwork" label="网络错误" valuePropName="checked"><Switch /></Form.Item></Col>
          </Row>
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <Space wrap>
            <Tag color="blue">模型数 {models.length}</Tag>
            {mode === 'fixed' ? <Tag color="purple">预计请求 {planned}</Tag> : <Tag color="purple">持续 {mode === 'duration' ? `${durationSec}s` : '爬坡'}</Tag>}
            {mode === 'fixed' && <Tag>并发 {concurrency}</Tag>}
          </Space>
          <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={submitting}>启动压测</Button>
        </div>
      </Form>
    </>
  )
}
