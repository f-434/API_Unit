import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, AutoComplete, Button, Col, Form, Input, InputNumber, Row, Select, Space,
  Switch, Tag, Tooltip, message,
} from 'antd'
import {
  DeleteOutlined, LinkOutlined, PictureOutlined, PlayCircleOutlined, PlusOutlined,
  SaveOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import { PageHeader } from '../components/common'
import { ModelMultiSelect, TargetSelect, useTargets } from '../components/TargetModel'

interface Row {
  targetId: string
  models: string[]
  label: string
  protocol: string
  kind: 'images' | 'chat'
  size: string
}

const emptyRow = (): Row => ({
  targetId: '', models: [], label: '', protocol: 'auto', kind: 'images', size: 'auto',
})

const FALLBACK_PHASES = [
  { name: 'generation_transparent', label: '文生图 · transparent', kind: 'images' },
  { name: 'generation_opaque', label: '文生图 · opaque', kind: 'images' },
  { name: 'generation_auto', label: '文生图 · auto', kind: 'images' },
  { name: 'edit_images_edits', label: '改图 · /v1/images/edits', kind: 'images' },
  { name: 'edit_alias_edits', label: '改图 · /v1/edits', kind: 'images' },
  { name: 'chat_generation', label: '聊天生图 · /v1/chat/completions', kind: 'chat' },
]

const DEFAULT_PHASES = FALLBACK_PHASES.map(p => p.name)
const SIZE_OPTIONS = ['auto', '1024x1024', '2048x2048', '4096x4096'].map(v => ({ value: v }))

const GENERATION_PROMPT = 'A cute cat sitting on a sofa'
const EDIT_PROMPT = 'Change the sofa color to blue, keep the cat and composition unchanged'

export default function ImageDetectionNew() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [form] = Form.useForm()
  const { data: targets = [] } = useTargets()
  const [rows, setRows] = useState<Row[]>([emptyRow()])
  const [phases, setPhases] = useState<string[]>(DEFAULT_PHASES)
  const [signatureLocal, setSignatureLocal] = useState(true)
  const [signatureOnline, setSignatureOnline] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [savingKey, setSavingKey] = useState(false)
  const [keyInput, setKeyInput] = useState('')
  const [baseUrl, setBaseUrl] = useState('https://api.openai.com')
  const [loaded, setLoaded] = useState(false)

  const { data: config } = useQuery({ queryKey: ['imageDetectionConfig'], queryFn: api.imageDetectionConfig })
  const phaseInfo = config?.phases?.length ? config.phases : FALLBACK_PHASES
  const hasKey = !!config?.settings?.hasKey
  const concurrency = Form.useWatch('concurrency', form)

  useEffect(() => {
    if (!config || loaded) return
    setBaseUrl(config.settings.baseUrl || 'https://api.openai.com')
    if (config.settings.hasKey) setSignatureOnline(true)
    setLoaded(true)
  }, [config, loaded])

  const validRows = useMemo(
    () => rows.filter(r => r.targetId && r.models.length > 0),
    [rows],
  )
  const modelCount = validRows.reduce((n, r) => n + r.models.length, 0)
  const estimatedPhases = useMemo(() => {
    let total = 0
    for (const r of validRows) {
      let perModel = 0
      if (r.kind === 'chat') {
        perModel = phases.includes('chat_generation') ? 1 : 0
      } else {
        const gens = ['generation_transparent', 'generation_opaque', 'generation_auto'].filter(p => phases.includes(p)).length
        const edits = ['edit_images_edits', 'edit_alias_edits'].filter(p => phases.includes(p)).length
        perModel = gens + edits + (edits > 0 && gens === 0 ? 1 : 0)
      }
      total += perModel * r.models.length
    }
    return total
  }, [validRows, phases])

  const setRow = (i: number, patch: Partial<Row>) => {
    setRows(prev => prev.map((r, idx) => idx === i ? { ...r, ...patch } : r))
  }

  const togglePhase = (name: string, checked: boolean) => {
    setPhases(prev => checked ? Array.from(new Set([...prev, name])) : prev.filter(p => p !== name))
  }

  const saveKey = async (clear = false) => {
    setSavingKey(true)
    try {
      const res = await api.saveImageDetectionConfig({
        baseUrl,
        apiKey: clear ? '' : keyInput,
        clearKey: clear,
      })
      setKeyInput('')
      setBaseUrl(res.settings.baseUrl)
      qc.setQueryData(['imageDetectionConfig'], res)
      if (clear) setSignatureOnline(false)
      else if (res.settings.hasKey) setSignatureOnline(true)
      message.success(clear ? '已清除在线验签 API Key' : '在线验签配置已保存')
    } catch (e: any) {
      message.error(e?.message || '保存失败')
    } finally {
      setSavingKey(false)
    }
  }

  const submit = async () => {
    if (!validRows.length) return message.error('请至少配置一个上游和模型')
    if (!phases.length) return message.error('请至少选择一个测试项')
    if (estimatedPhases === 0) return message.error('当前测试项与模型类型不匹配，请调整测试项或模型类型')
    setSubmitting(true)
    try {
      const v = form.getFieldsValue()
      const configBody = {
        targets: validRows.map(r => ({
          targetId: r.targetId,
          models: r.models.map(name => ({
            name,
            kind: r.kind,
            size: r.size === 'auto' ? '' : r.size,
          })),
          protocol: r.protocol || 'auto',
          label: r.label || undefined,
        })),
        concurrency: Number(v.concurrency || 2),
        timeoutMs: Number(v.timeoutSec || 180) * 1000,
        retry: {
          maxRetries: Number(v.maxRetries ?? 1),
          backoffMs: Number(v.backoffMs ?? 800),
          maxBackoffMs: Number(v.maxBackoffMs ?? 5000),
          retryOn429: true,
          retryOn5xx: true,
          retryOnNetwork: true,
        },
        phases,
        generationPrompt: v.generationPrompt,
        editPrompt: v.editPrompt,
        outputFormat: v.outputFormat || 'png',
        signature: { local: signatureLocal, online: signatureOnline },
      }
      const res = await api.createRun('image_detection', configBody)
      message.success('生图检测已启动')
      qc.invalidateQueries({ queryKey: ['runs'] })
      navigate(`/runs/${res.id}`)
    } catch (e: any) {
      message.error(e?.message || '启动失败')
    } finally {
      setSubmitting(false)
    }
  }

  const imagePhases = phaseInfo.filter(p => p.kind === 'images')
  const chatPhases = phaseInfo.filter(p => p.kind === 'chat')

  return (
    <>
      <PageHeader
        title={<Space><PictureOutlined /> 新建生图检测</Space>}
        subtitle="真实调用 OpenAI Images 兼容接口，验证尺寸、透明背景与改图能力；图片按原始字节保存，并检查 C2PA 元数据与 SynthID 水印。"
      />

      <Form
        form={form}
        layout="vertical"
        initialValues={{
          concurrency: 2,
          timeoutSec: 180,
          maxRetries: 1,
          backoffMs: 800,
          maxBackoffMs: 5000,
          outputFormat: 'png',
          generationPrompt: GENERATION_PROMPT,
          editPrompt: EDIT_PROMPT,
        }}
      >
        <div className="panel">
          <div className="panel-title">1. 测试目标</div>
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
                <Col xs={24} lg={7}>
                  <ModelMultiSelect
                    targetId={row.targetId}
                    value={row.models}
                    onChange={v => setRow(i, { models: v || [] })}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <Select
                    value={row.kind}
                    onChange={v => setRow(i, { kind: v })}
                    options={[
                      { value: 'images', label: '图片接口' },
                      { value: 'chat', label: '聊天接口' },
                    ]}
                    style={{ width: '100%' }}
                  />
                </Col>
                <Col xs={12} lg={3}>
                  <AutoComplete
                    value={row.size}
                    options={SIZE_OPTIONS}
                    onChange={v => setRow(i, { size: v || 'auto' })}
                    placeholder="auto / 1024x1024"
                    style={{ width: '100%' }}
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
                  <Input value={row.label} onChange={e => setRow(i, { label: e.target.value })} placeholder="标签（可选）" />
                </Col>
                <Col flex="auto" style={{ textAlign: 'right' }}>
                  <Button
                    danger
                    type="text"
                    icon={<DeleteOutlined />}
                    disabled={rows.length === 1}
                    onClick={() => setRows(prev => prev.filter((_, idx) => idx !== i))}
                  />
                </Col>
              </Row>
              <div className="muted" style={{ marginTop: 8, fontSize: 12 }}>
                {row.kind === 'images'
                  ? '图片接口：走 /v1/images/generations 与 /v1/images/edits（或 /v1/edits）。尺寸留空时按模型名推断：4k→4096、2k→2048，其余 1024。'
                  : '聊天接口：走 /v1/chat/completions，从响应中提取 data URL、markdown 图片或 images 字段。'}
              </div>
            </div>
          ))}
          <Button type="dashed" block icon={<PlusOutlined />} onClick={() => setRows(prev => [...prev, emptyRow()])}>
            添加上游
          </Button>
          <div className="muted" style={{ marginTop: 10, fontSize: 12 }}>
            生图、改图与聊天生图均按 OpenAI 兼容路径调用；协议选择只用于报告标注，认证统一使用 Bearer API Key。
          </div>
        </div>

        <div className="panel">
          <div className="panel-title">2. 测试项与参数</div>
          <div className="muted" style={{ marginBottom: 6 }}>图片接口测试项</div>
          <Space wrap style={{ marginBottom: 10 }}>
            {imagePhases.map(p => (
              <Tag.CheckableTag
                key={p.name}
                checked={phases.includes(p.name)}
                onChange={checked => togglePhase(p.name, checked)}
              >
                {p.label}
              </Tag.CheckableTag>
            ))}
          </Space>
          <div className="muted" style={{ marginBottom: 6 }}>聊天接口测试项</div>
          <Space wrap style={{ marginBottom: 12 }}>
            {chatPhases.map(p => (
              <Tag.CheckableTag
                key={p.name}
                checked={phases.includes(p.name)}
                onChange={checked => togglePhase(p.name, checked)}
              >
                {p.label}
              </Tag.CheckableTag>
            ))}
          </Space>
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 14 }}
            message="测试项与模型类型自动匹配"
            description="图片接口模型只执行文生图/改图，聊天接口模型只执行聊天生图；不匹配的测试项会被跳过。只勾选改图时会自动补跑一次 transparent 文生图作为改图输入。"
          />

          <Row gutter={[16, 10]}>
            <Col xs={12} lg={4}><Form.Item name="concurrency" label="模型并发"><InputNumber min={1} max={16} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="timeoutSec" label="单请求超时（秒）"><InputNumber min={10} max={900} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxRetries" label="最大重试"><InputNumber min={0} max={5} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="backoffMs" label="退避基数 ms"><InputNumber min={0} max={60000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}><Form.Item name="maxBackoffMs" label="最大退避 ms"><InputNumber min={0} max={120000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} lg={4}>
              <Form.Item name="outputFormat" label="输出格式">
                <Select options={[{ value: 'png', label: 'png' }, { value: 'jpeg', label: 'jpeg' }, { value: 'webp', label: 'webp' }]} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={[16, 10]}>
            <Col xs={24} lg={12}>
              <Form.Item name="generationPrompt" label="文生图提示词">
                <Input.TextArea autoSize={{ minRows: 2, maxRows: 4 }} />
              </Form.Item>
            </Col>
            <Col xs={24} lg={12}>
              <Form.Item name="editPrompt" label="改图提示词">
                <Input.TextArea autoSize={{ minRows: 2, maxRows: 4 }} />
              </Form.Item>
            </Col>
          </Row>
        </div>

        <div className="panel">
          <div className="panel-title">3. 来源签名检测（C2PA / SynthID）</div>
          <Row gutter={[16, 12]} align="middle">
            <Col xs={12} lg={5}>
              <Space size={6}>
                <Switch checked={signatureLocal} onChange={setSignatureLocal} />
                <span>本地 C2PA 元数据</span>
              </Space>
              <div className="muted" style={{ fontSize: 12 }}>解析 JUMBF / caBX 清单，不做验签</div>
            </Col>
            <Col xs={12} lg={5}>
              <Space size={6}>
                <Switch checked={signatureOnline} onChange={setSignatureOnline} />
                <span>在线双签名校验</span>
              </Space>
              <div className="muted" style={{ fontSize: 12 }}>调用 OpenAI Content Provenance API</div>
            </Col>
            <Col xs={24} lg={14}>
              <Space wrap>
                <Tag color={hasKey ? 'green' : 'default'}>{hasKey ? 'API Key 已配置' : 'API Key 未配置'}</Tag>
                <Tag>Endpoint: {config?.settings?.endpoint || 'https://api.openai.com/v1/content_provenance_checks'}</Tag>
                {config?.settings?.keyMasked && <Tag>{config.settings.keyMasked}</Tag>}
              </Space>
              <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
                在线校验会同时返回 C2PA 与 SynthID 两类结果；未配置 Key 时仍可使用报告中的第三方校验入口手动上传。
              </div>
            </Col>
          </Row>

          <Row gutter={[10, 10]} align="middle" style={{ marginTop: 12 }}>
            <Col xs={24} lg={8}>
              <Input value={baseUrl} onChange={e => setBaseUrl(e.target.value)} placeholder="https://api.openai.com" />
            </Col>
            <Col xs={24} lg={8}>
              <Input.Password
                value={keyInput}
                onChange={e => setKeyInput(e.target.value)}
                placeholder={hasKey ? '输入新 Key 可覆盖，留空保持不变' : 'sk-...'}
                autoComplete="new-password"
              />
            </Col>
            <Col xs={24} lg={8}>
              <Space>
                <Button icon={<SaveOutlined />} loading={savingKey} onClick={() => saveKey(false)}>保存配置</Button>
                <Button danger type="text" disabled={!hasKey} onClick={() => saveKey(true)}>清除 Key</Button>
              </Space>
            </Col>
          </Row>

          <Space wrap style={{ marginTop: 12 }}>
            <Button size="small" icon={<LinkOutlined />} href="https://contentauthenticity.adobe.com/inspect" target="_blank">Adobe Content Credentials</Button>
            <Button size="small" icon={<LinkOutlined />} href="https://openai.com/zh-Hans-CN/research/verify/" target="_blank">OpenAI 内容验证</Button>
            <Button size="small" icon={<LinkOutlined />} href="https://ai.azure.com/nextgen/validate" target="_blank">Azure AI 内容验证</Button>
          </Space>

          {signatureOnline && !hasKey && (
            <Alert
              style={{ marginTop: 12 }}
              type="warning"
              showIcon
              message="已启用在线校验，但尚未配置 API Key"
              description="本次仍会保存图片并执行本地 C2PA 检查；在线结果会标记为未配置。配置 Key 后可自动同时校验 C2PA 与 SynthID。"
            />
          )}
          <Alert
            style={{ marginTop: 12 }}
            type="info"
            showIcon
            message="签名缺失不等于造假"
            description="平台转码、缩放、截图或上游重编码都可能移除 C2PA 元数据并削弱 SynthID 水印。在线校验结果只说明是否检测到受支持的签名信号。"
          />
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <Space wrap>
            <Tag color="magenta">模型 {modelCount}</Tag>
            <Tag color="purple">预计测试项 {estimatedPhases}</Tag>
            <Tag color="cyan">并发 {concurrency || 2}</Tag>
            <Tag color={signatureOnline ? 'green' : 'gold'}>在线验签 {signatureOnline ? '启用' : '关闭'}</Tag>
          </Space>
          <Tooltip title={!modelCount ? '请先选择上游和模型' : estimatedPhases === 0 ? '当前测试项与模型类型不匹配' : ''}>
            <Button
              type="primary"
              size="large"
              icon={<PlayCircleOutlined />}
              loading={submitting}
              disabled={!modelCount || !phases.length || estimatedPhases === 0}
              onClick={submit}
            >
              启动生图检测
            </Button>
          </Tooltip>
        </div>
      </Form>
    </>
  )
}
