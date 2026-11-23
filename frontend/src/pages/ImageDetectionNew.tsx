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
import { PageHeader, SummaryLine } from '../components/common'
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
  const [proxyInput, setProxyInput] = useState('')
  const [forceTls12, setForceTls12] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const { data: config } = useQuery({ queryKey: ['imageDetectionConfig'], queryFn: api.imageDetectionConfig })
  const phaseInfo = config?.phases?.length ? config.phases : FALLBACK_PHASES
  const hasKey = !!config?.settings?.hasKey
  const concurrency = Form.useWatch('concurrency', form)

  useEffect(() => {
    if (!config || loaded) return
    setBaseUrl(config.settings.baseUrl || 'https://api.openai.com')
    setForceTls12(!!config.settings.forceTls12)
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

  // clearKey / clearProxy 用于显式清除；留空的输入框表示保持原值。
  const saveKey = async (clearKey = false, clearProxy = false) => {
    setSavingKey(true)
    try {
      const res = await api.saveImageDetectionConfig({
        baseUrl,
        // 清除某一项时不要顺带提交另一项的输入，避免误改。
        apiKey: clearKey || clearProxy ? '' : keyInput,
        clearKey,
        proxyUrl: clearKey || clearProxy ? '' : proxyInput,
        clearProxy,
        forceTls12,
      })
      setKeyInput('')
      setProxyInput('')
      setBaseUrl(res.settings.baseUrl)
      qc.setQueryData(['imageDetectionConfig'], res)
      if (clearKey) setSignatureOnline(false)
      else if (res.settings.hasKey) setSignatureOnline(true)
      message.success(clearKey ? '已清除在线验签 API Key' : clearProxy ? '已清除生图请求代理' : '生图检测配置已保存')
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
        timeoutMs: Number(v.timeoutSec || 600) * 1000,
        retry: {
          maxRetries: Number(v.maxRetries ?? 0),
          backoffMs: Number(v.backoffMs ?? 800),
          maxBackoffMs: Number(v.maxBackoffMs ?? 5000),
          retryOn429: !!v.retryOn429,
          retryOn5xx: !!v.retryOn5xx,
          retryOnNetwork: !!v.retryOnNetwork,
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
      <PageHeader title={<Space><PictureOutlined /> 新建生图检测</Space>} />

      <Form
        form={form}
        layout="vertical"
        initialValues={{
          concurrency: 2,
          // 生图响应体是几 MB 的 base64，默认给足下载时间并关闭重试，
          // 避免超时后再等一倍时间。可在页面上按需调整。
          timeoutSec: 600,
          maxRetries: 0,
          backoffMs: 800,
          maxBackoffMs: 5000,
          retryOn429: true,
          retryOn5xx: true,
          retryOnNetwork: true,
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
            </div>
          ))}
          <Button type="dashed" block icon={<PlusOutlined />} onClick={() => setRows(prev => [...prev, emptyRow()])}>
            添加上游
          </Button>
        </div>

        <div className="panel">
          <div className="panel-title">2. 测试项与参数</div>
          <div className="field-label">图片接口</div>
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
          <div className="field-label" style={{ marginTop: 8 }}>聊天接口</div>
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
            <Col xs={8} lg={4}><Form.Item name="retryOn429" label="重试 429" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={4}><Form.Item name="retryOn5xx" label="重试 5xx" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={8} lg={4}><Form.Item name="retryOnNetwork" label="重试网络错误" valuePropName="checked"><Switch /></Form.Item></Col>
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
          <div className="panel-title">3. 来源签名</div>
          <Row gutter={[16, 12]} align="middle">
            <Col xs={12} lg={5}>
              <Space size={6}>
                <Switch checked={signatureLocal} onChange={setSignatureLocal} />
                <span>本地 C2PA 元数据</span>
              </Space>
            </Col>
            <Col xs={12} lg={5}>
              <Space size={6}>
                <Switch checked={signatureOnline} onChange={setSignatureOnline} />
                <span>在线双签名校验</span>
              </Space>
            </Col>
            <Col xs={24} lg={14}>
              <Tag color={hasKey ? 'green' : 'default'}>{hasKey ? 'API Key 已配置' : 'API Key 未配置'}</Tag>
            </Col>
          </Row>

          <Row gutter={[10, 10]} align="top" style={{ marginTop: 12 }}>
            <Col xs={24} lg={8}>
              <div className="field-label">在线验签 Base URL</div>
              <Input value={baseUrl} onChange={e => setBaseUrl(e.target.value)} placeholder="https://api.openai.com" />
            </Col>
            <Col xs={24} lg={8}>
              <div className="field-label">在线验签 API Key</div>
              <Input.Password
                value={keyInput}
                onChange={e => setKeyInput(e.target.value)}
                placeholder={hasKey ? '留空保持不变' : 'sk-...'}
                autoComplete="new-password"
              />
            </Col>
            <Col xs={24} lg={8}>
              <div className="field-label">生图代理（http / https / socks5）</div>
              <Input
                value={proxyInput}
                onChange={e => setProxyInput(e.target.value)}
                placeholder={config?.settings?.proxyConfigured
                  ? `已配置 ${config.settings.proxyMasked}，留空保持不变`
                  : 'http://127.0.0.1:7890'}
                autoComplete="off"
              />
            </Col>
          </Row>
          <Row gutter={[10, 10]} align="middle" style={{ marginTop: 10 }}>
            <Col xs={24} lg={10}>
              <Space size={6}>
                <Switch checked={forceTls12} onChange={setForceTls12} />
                <span>强制 TLS 1.2</span>
              </Space>
            </Col>
            <Col xs={24} lg={14}>
              <Space wrap>
                <Button icon={<SaveOutlined />} loading={savingKey} onClick={() => saveKey(false)}>保存配置</Button>
                <Button danger type="text" disabled={!hasKey} onClick={() => saveKey(true)}>清除 Key</Button>
                <Button danger type="text" disabled={!config?.settings?.proxyConfigured} onClick={() => saveKey(false, true)}>清除代理</Button>
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
              description="本次仍会保存图片并执行本地 C2PA 检查；在线结果会标记为未配置。"
            />
          )}
        </div>

        <div className="panel" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <SummaryLine items={[
            { label: '模型', value: modelCount },
            { label: '预计测试项', value: estimatedPhases },
            { label: '并发', value: concurrency || 2 },
          ]} />
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
