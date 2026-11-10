import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, Input, InputNumber, Row, Segmented, Select, Space, Switch, Table, Tag, message,
} from 'antd'
import { PlayCircleOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Attack } from '../types'
import { PageHeader, SeverityTag, EllipsisText } from '../components/common'
import { ModelAutoComplete, TargetRowsEditor, TargetSelect, useTargets, type TargetRow } from '../components/TargetModel'
import { categoryLabel } from '../format'

export default function InjectionNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [cat, setCat] = useState<string>()
  const [sev, setSev] = useState<string>()
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const { data: targets = [] } = useTargets()
  const { data: attackData, isLoading: attacksLoading } = useQuery({ queryKey: ['attacks'], queryFn: api.attacks })
  const attacks = attackData?.items || []
  const categories = attackData?.categories || []
  const targetRows = (Form.useWatch('targets', form) || []) as TargetRow[]
  const mode = Form.useWatch('judgeMode', form) || 'hybrid'
  const judgeTargetId = Form.useWatch('judgeTargetId', form)
  const judgeModel = Form.useWatch('judgeModel', form)
  const reps = Form.useWatch('repetitions', form) || 1

  const filtered = useMemo(() => attacks.filter(a => (!cat || a.category === cat) && (!sev || a.severity === sev)), [attacks, cat, sev])
  const validTargets = targetRows.filter(t => t.targetId && t.model).length
  const planned = validTargets * selected.length * Number(reps || 0)

  const submit = async (v: any) => {
    const rows = (v.targets || []).filter((t: TargetRow) => t.targetId && t.model)
    if (!rows.length) return message.error('请至少配置一个有效的被测目标')
    if (!selected.length) return message.error('请选择至少一个攻击用例')
    if (v.judgeMode === 'llm' && (!v.judgeTargetId || !v.judgeModel)) return message.error('LLM 裁判模式需要选择裁判上游与模型')
    setSubmitting(true)
    try {
      const config = {
        targets: rows.map((t: TargetRow) => ({
          targetId: t.targetId, model: t.model, protocol: t.protocol || 'auto', label: t.label || undefined,
        })),
        attackIds: selected,
        repetitions: v.repetitions,
        concurrency: v.concurrency,
        timeoutMs: Number(v.timeoutSec || 120) * 1000,
        maxOutputTokens: v.maxOutputTokens,
        temperature: v.temperature ?? 0.7,
        stream: !!v.stream,
        judge: {
          mode: v.judgeMode,
          targetId: v.judgeTargetId || '',
          model: v.judgeModel || '',
          maxTokens: v.judgeMaxTokens || 400,
          timeoutMs: Number(v.judgeTimeoutSec || 60) * 1000,
          rubric: v.judgeRubric || '',
        },
        retry: {
          maxRetries: v.maxRetries || 0,
          backoffMs: v.backoffMs || 500,
          maxBackoffMs: v.maxBackoffMs || 5000,
          retryOn429: !!v.retryOn429,
          retryOn5xx: !!v.retryOn5xx,
          retryOnNetwork: !!v.retryOnNetwork,
        },
      }
      const res = await api.createRun('injection', config)
      message.success('注入测试已启动')
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
        title="新建提示词注入安全测试"
        subtitle="规则判定 + 可选 LLM 裁判。规则模式零额外成本；混合模式只在规则无法判定时调用裁判，兼顾准确率与成本。"
      />
      <Form
        form={form}
        layout="vertical"
        onFinish={submit}
        initialValues={{
          targets: [{ targetId: '', model: '', label: '', protocol: 'auto' }],
          repetitions: 1, concurrency: 4, timeoutSec: 120, maxOutputTokens: 512, temperature: 0.7, stream: false,
          judgeMode: 'hybrid', judgeTargetId: undefined, judgeModel: '', judgeMaxTokens: 400, judgeTimeoutSec: 60, judgeRubric: '',
          maxRetries: 0, backoffMs: 500, maxBackoffMs: 5000, retryOn429: true, retryOn5xx: true, retryOnNetwork: true,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 被测目标</div>
          <Form.Item name="targets" rules={[{ required: true }]}>
            <TargetRowsEditor tokenizers={meta?.tokenizerStrategies || []} showTokenizer={false} />
          </Form.Item>
        </div>

        <div className="panel">
          <div className="panel-title">2. 攻击用例</div>
          <Space wrap style={{ marginBottom: 10 }}>
            <Select allowClear placeholder="分类" value={cat} onChange={setCat} style={{ width: 170 }} options={categories.map(c => ({ value: c.id, label: c.label }))} />
            <Select allowClear placeholder="严重度" value={sev} onChange={setSev} style={{ width: 120 }} options={['critical', 'high', 'medium', 'low'].map(s => ({ value: s, label: s }))} />
            <Button size="small" onClick={() => setSelected(filtered.map(a => a.id))}>全选当前筛选</Button>
            <Button size="small" onClick={() => setSelected([])}>清空</Button>
            <Tag color="purple">已选 {selected.length} / {attacks.length}</Tag>
          </Space>
          <Table
            rowKey="id"
            size="small"
            loading={attacksLoading}
            dataSource={filtered}
            pagination={{ pageSize: 8, showSizeChanger: false }}
            rowSelection={{ selectedRowKeys: selected, onChange: keys => setSelected(keys as string[]) }}
            columns={[
              { title: '用例', dataIndex: 'name', width: 230 },
              { title: '分类', dataIndex: 'category', width: 130, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
              { title: '严重度', dataIndex: 'severity', width: 90, render: (v: string) => <SeverityTag severity={v} /> },
              { title: '判定', dataIndex: 'requireJudge', width: 100, render: (v: boolean) => v ? <Tag color="purple">需裁判</Tag> : <Tag>规则</Tag> },
              { title: '提示词', dataIndex: 'prompt', render: (v: string) => <EllipsisText text={v} width={420} /> },
            ]}
          />
        </div>

        <div className="panel">
          <div className="panel-title">3. 裁判配置</div>
          <Form.Item name="judgeMode" label="判定模式">
            <Segmented options={[
              { value: 'rule', label: '仅规则（零成本）' },
              { value: 'hybrid', label: '混合（推荐）' },
              { value: 'llm', label: '全部 LLM 裁判' },
            ]} />
          </Form.Item>
          <Row gutter={[14, 6]}>
            <Col xs={24} lg={8}>
              <Form.Item name="judgeTargetId" label="裁判上游">
                <TargetSelect targets={targets} placeholder="选择裁判模型所在上游" />
              </Form.Item>
            </Col>
            <Col xs={24} lg={8}>
              <Form.Item name="judgeModel" label="裁判模型">
                <ModelAutoComplete targetId={judgeTargetId} value={judgeModel} onChange={v => form.setFieldValue('judgeModel', v)} placeholder="例如 gpt-4o-mini / gemini-2.5-flash" />
              </Form.Item>
            </Col>
            <Col xs={12} lg={4}><Form.Item name="judgeMaxTokens" label="裁判输出上限"><InputNumber min={64} max={4096} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="judgeTimeoutSec" label="裁判超时（秒）"><InputNumber min={5} max={600} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Form.Item name="judgeRubric" label="全局裁判要点（可选）" extra="会与用例自带的判定要点一起送入裁判模型。">
            <Input.TextArea rows={2} placeholder="例如：只要模型明确拒绝或坚持系统约束，即判定为 blocked。" />
          </Form.Item>
          {mode === 'llm' && (!judgeTargetId) && <Alert type="warning" showIcon message="全部 LLM 裁判模式必须配置裁判上游与模型。" />}
        </div>

        <div className="panel">
          <div className="panel-title">4. 执行参数</div>
          <Row gutter={[14, 6]}>
            <Col xs={12} lg={3}><Form.Item name="repetitions" label="重复次数"><InputNumber min={1} max={10} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="concurrency" label="并发"><InputNumber min={1} max={32} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="timeoutSec" label="超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="maxOutputTokens" label="输出上限"><InputNumber min={16} max={8192} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="temperature" label="temperature"><InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={3}><Form.Item name="stream" label="流式" valuePropName="checked"><Switch /></Form.Item></Col>
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
          <Space wrap>
            <Tag color="blue">目标 {validTargets}</Tag>
            <Tag color="red">用例 {selected.length}</Tag>
            <Tag color="gold">重复 {reps}</Tag>
            <Tag color="purple">预计请求 {planned}</Tag>
            <Tag icon={<SafetyCertificateOutlined />}>ASR 分母不含执行失败</Tag>
          </Space>
          <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={submitting}>启动测试</Button>
        </div>
      </Form>
    </>
  )
}
