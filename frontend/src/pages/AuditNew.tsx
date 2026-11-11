import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Col, Form, InputNumber, Input, Row, Select, Space, Spin, Switch, Tag, Tooltip, message,
} from 'antd'
import { CheckOutlined, EditOutlined, PlayCircleOutlined, UndoOutlined } from '@ant-design/icons'
import { api } from '../api'
import { PageHeader } from '../components/common'
import { TargetRowsEditor, type TargetRow } from '../components/TargetModel'
import type { Prompt } from '../types'
import { fmtInt } from '../format'

type TokenEstimate = { tokens: number; source: string; error?: string }

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
  const maxOutputTokens = Form.useWatch('maxOutputTokens', form) || 8

  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [editingIds, setEditingIds] = useState<Set<string>>(new Set())
  const [savingId, setSavingId] = useState<string>()
  const [estimates, setEstimates] = useState<Record<string, TokenEstimate>>({})
  const [estimating, setEstimating] = useState(false)

  const promptMap = useMemo(() => {
    const map: Record<string, Prompt> = {}
    for (const p of prompts?.items || []) map[p.id] = p
    return map
  }, [prompts])

  const selectedPrompts = useMemo(
    () => promptIds.map(id => promptMap[id]).filter(Boolean) as Prompt[],
    [promptIds, promptMap],
  )

  const validTargets = useMemo(
    () => targets.map((t, ti) => ({ ...t, ti })).filter(t => t.targetId && t.model),
    [targets],
  )

  // Seed drafts for newly selected prompts while preserving in-progress edits.
  // If the prompt library is still loading we leave the key unset so the
  // content can be seeded once the query resolves.
  useEffect(() => {
    setDrafts(prev => {
      const next: Record<string, string> = {}
      for (const id of promptIds) {
        if (prev[id] !== undefined) next[id] = prev[id]
        else if (promptMap[id]) next[id] = promptMap[id].content
      }
      return next
    })
  }, [promptIds.join('|'), prompts])

  const estimateSig = useMemo(() => JSON.stringify({
    targets: validTargets.map(t => [t.ti, t.targetId, t.model, t.tokenizer || 'auto']),
    prompts: selectedPrompts.map(p => [p.id, drafts[p.id] ?? p.content]),
  }), [validTargets, selectedPrompts, drafts])

  // Count each selected prompt against every configured target. "online"
  // counting is replaced with a local strategy so typing does not hit upstream.
  useEffect(() => {
    let cancelled = false
    const jobs: { key: string; targetId: string; model: string; strategy: string; content: string }[] = []
    for (const t of validTargets) {
      for (const p of selectedPrompts) {
        const content = drafts[p.id] ?? p.content
        if (!content.trim()) continue
        const strategy = t.tokenizer === 'online' ? 'auto' : (t.tokenizer || 'auto')
        jobs.push({ key: `${t.ti}:${p.id}`, targetId: t.targetId, model: t.model, strategy, content })
      }
    }
    if (!jobs.length) {
      setEstimates({})
      setEstimating(false)
      return
    }
    setEstimating(true)
    const timer = setTimeout(() => {
      Promise.all(jobs.map(async (j): Promise<[string, TokenEstimate]> => {
        try {
          const r = await api.tokenizerCount({
            model: j.model, strategy: j.strategy, targetId: j.targetId,
            messages: [{ role: 'user', content: j.content }],
          })
          return [j.key, { tokens: Number(r?.tokens || 0), source: String(r?.source || '') }]
        } catch (e: any) {
          return [j.key, { tokens: 0, source: '', error: e?.message || '估算失败' }]
        }
      })).then(entries => {
        if (!cancelled) {
          setEstimates(Object.fromEntries(entries))
          setEstimating(false)
        }
      })
    }, 350)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [estimateSig])

  const estimateErrors = useMemo(() => {
    const out = new Set<string>()
    for (const e of Object.values(estimates)) if (e.error) out.add(e.error)
    return Array.from(out)
  }, [estimates])

  const estimateFailed = validTargets.length > 0 && selectedPrompts.length > 0 && !estimating &&
    Object.keys(estimates).length > 0 && Object.values(estimates).every(e => !!e.error)

  const estimate = useMemo(() => {
    const perTarget = validTargets.map(t => {
      let tokens = 0
      let missing = false
      for (const p of selectedPrompts) {
        const e = estimates[`${t.ti}:${p.id}`]
        if (!e || e.error) missing = true
        else tokens += e.tokens
      }
      return { key: `${t.ti}`, label: t.label || `目标 ${t.ti + 1}`, model: t.model, tokens, missing }
    })
    const perRound = perTarget.reduce((n, t) => n + t.tokens, 0)
    const repsN = Number(reps || 0)
    const requests = validTargets.length * selectedPrompts.length * repsN
    const totalInput = perRound * repsN
    const avgInput = requests > 0 ? totalInput / requests : 0
    const outputUpper = Number(maxOutputTokens || 0) * requests
    return { perTarget, perRound, requests, totalInput, avgInput, outputUpper }
  }, [validTargets, selectedPrompts, estimates, reps, maxOutputTokens])

  const planned = useMemo(() => {
    return validTargets.length * promptIds.length * Number(reps || 0)
  }, [validTargets, promptIds, reps])

  const estimateTip = (
    <div style={{ lineHeight: 1.7, maxWidth: 420 }}>
      {estimateErrors.length > 0 && (
        <div style={{ color: '#dc2626', whiteSpace: 'pre-wrap' }}>
          估算失败：{estimateErrors.join('；')}
        </div>
      )}
      <div>全轮估算 = Σ(每条提示词输入 Token) × 目标数 × 重复次数。</div>
      {estimate.perTarget.map(t => (
        <div key={t.key}>{t.label}（{t.model}）：{fmtInt(t.tokens)} tok/轮{t.missing ? ' · 部分未估算' : ''}</div>
      ))}
      <div>请求数 {fmtInt(estimate.requests)} · 平均输入 {fmtInt(estimate.avgInput)} tok/请求</div>
      <div>输出上限 {fmtInt(estimate.outputUpper)} tok</div>
      <div className="muted">在线计数策略会改用本地估算，避免实时打字触发上游请求。</div>
    </div>
  )

  const toggleEdit = (id: string) => setEditingIds(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  const resetDraft = (p: Prompt) => setDrafts(prev => ({ ...prev, [p.id]: p.content }))

  const saveDraft = async (p: Prompt) => {
    const content = drafts[p.id] ?? p.content
    if (!content.trim()) return message.error('提示词内容不能为空')
    setSavingId(p.id)
    try {
      await api.updatePrompt(p.id, { name: p.name, category: p.category, content })
      message.success('已保存到提示词库')
      qc.invalidateQueries({ queryKey: ['prompts'] })
    } catch (e: any) {
      message.error(e?.message || '保存失败')
    } finally {
      setSavingId(undefined)
    }
  }

  const submit = async (v: any) => {
    const rows = (v.targets || []).filter((t: TargetRow) => t.targetId && t.model)
    if (!rows.length) return message.error('请至少配置一个有效的对比目标')
    if (!(v.promptIds || []).length) return message.error('请选择至少一个审计提示词')
    const empty = selectedPrompts.find(p => !(drafts[p.id] ?? p.content).trim())
    if (empty) return message.error(`提示词「${empty.name}」内容不能为空`)
    const dirty = selectedPrompts.filter(p => (drafts[p.id] ?? p.content) !== p.content)
    setSubmitting(true)
    try {
      if (dirty.length) {
        try {
          await Promise.all(dirty.map(p => api.updatePrompt(p.id, {
            name: p.name, category: p.category, content: drafts[p.id] ?? p.content,
          })))
        } catch (e: any) {
          message.error(e?.message || '保存提示词失败')
          setSubmitting(false)
          return
        }
        qc.invalidateQueries({ queryKey: ['prompts'] })
      }
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
          <div className="panel-title" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
            <span>2. 审计提示词</span>
            <Tooltip title={validTargets.length && selectedPrompts.length ? estimateTip : '选择对比目标和提示词后自动估算'}>
              <Tag color={estimateFailed ? 'red' : 'blue'} style={{ fontWeight: 400, marginInlineEnd: 0 }}>
                {estimating && <Spin size="small" style={{ marginRight: 6 }} />}
                {validTargets.length && selectedPrompts.length
                  ? estimateFailed ? '预估失败' : `预估输入（全轮）≈ ${fmtInt(estimate.totalInput)} tok`
                  : '预估输入（全轮）≈ - tok'}
              </Tag>
            </Tooltip>
          </div>
          <Form.Item name="promptIds" label="选择提示词（建议覆盖短文本、长文本、中英混合）" rules={[{ required: true, message: '请选择提示词' }]}>
            <Select mode="multiple" allowClear placeholder="从提示词库选择" options={(prompts?.items || []).map(p => ({ value: p.id, label: `${p.name}（${p.content.length} 字）` }))} />
          </Form.Item>

          {selectedPrompts.length > 0 && (
            <div style={{ marginBottom: 14 }}>
              <div className="muted" style={{ marginTop: -6, marginBottom: 8, fontSize: 12 }}>
                可直接编辑选中提示词；保存会写回提示词库。启动审计前会自动保存未保存的修改。
              </div>
              {selectedPrompts.map(p => {
                const content = drafts[p.id] ?? p.content
                const dirty = content !== p.content
                const editing = editingIds.has(p.id)
                let tokens = 0
                let missing = false
                let errored = 0
                for (const t of validTargets) {
                  const e = estimates[`${t.ti}:${p.id}`]
                  if (!e) { missing = true; continue }
                  if (e.error) { errored++; missing = true; continue }
                  tokens += e.tokens
                }
                const allFailed = validTargets.length > 0 && errored === validTargets.length && !estimating
                const tokenLabel = !validTargets.length
                  ? '先选择对比目标'
                  : estimating ? '估算中…'
                    : allFailed ? '估算失败'
                      : `≈ ${fmtInt(tokens)} tok/轮${missing ? '（部分失败）' : ''}`
                return (
                  <div key={p.id} className="case-card" style={{ marginTop: 0, marginBottom: 8, padding: editing ? 12 : '8px 12px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, flexWrap: 'wrap' }}>
                      <Space size={6} wrap>
                        <b>{p.name}</b>
                        {p.builtin && <Tag color="blue">内置</Tag>}
                        {dirty && <Tag color="gold">未保存</Tag>}
                        <span className="muted">{content.length} 字</span>
                        <Tag color={allFailed ? 'red' : missing ? 'orange' : 'default'}>{tokenLabel}</Tag>
                      </Space>
                      <Space size={4}>
                        {dirty && <Button size="small" icon={<UndoOutlined />} onClick={() => resetDraft(p)}>重置</Button>}
                        {dirty && <Button size="small" type="primary" icon={<CheckOutlined />} loading={savingId === p.id} onClick={() => saveDraft(p)}>保存</Button>}
                        <Button size="small" type="link" icon={<EditOutlined />} onClick={() => toggleEdit(p.id)}>{editing ? '收起' : '编辑'}</Button>
                      </Space>
                    </div>
                    {editing && (
                      <Input.TextArea
                        style={{ marginTop: 8 }}
                        autoSize={{ minRows: 2, maxRows: 8 }}
                        className="code-input"
                        value={content}
                        onChange={e => setDrafts(prev => ({ ...prev, [p.id]: e.target.value }))}
                      />
                    )}
                  </div>
                )
              })}
            </div>
          )}

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
            <Tag color="blue">目标 {validTargets.length}</Tag>
            <Tag color="gold">提示词 {promptIds.length}</Tag>
            <Tag color="purple">重复 {reps}</Tag>
            <Tag color="magenta">预计请求 {planned}</Tag>
            <Tag color="cyan">预估输入（全轮）{validTargets.length && selectedPrompts.length ? fmtInt(estimate.totalInput) : '-'} tok</Tag>
          </Space>
          <Button type="primary" size="large" icon={<PlayCircleOutlined />} htmlType="submit" loading={submitting}>启动审计</Button>
        </div>
      </Form>
    </>
  )
}
