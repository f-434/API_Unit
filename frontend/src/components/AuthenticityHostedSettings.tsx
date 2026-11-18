import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Col, Collapse, Input, Row, Select, Space, Switch, Tag, message } from 'antd'
import { SaveOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { AuthenticityHostedConfig } from '../types'

const defaults: AuthenticityHostedConfig = {
  profile: 'generic',
  defaultEnabled: false,
  providers: { openai: true, claude: true, gemini: true },
  mcp: { serverLabel: 'proof', serverDescription: '只读证明服务：返回本次校验的 nonce 与记录。', serverUrl: '', headers: {}, requireApproval: 'never', toolName: 'get_proof_record' },
  proof: { baseUrl: '', domain: '', pathTemplate: '/proof/{runToken}', expectedNonceField: 'secret' },
  legacy: {
    domain: '', envVar: 'MODEL_AUTHENTICITY_STATUS', expectedEnvValue: 'verified',
    collection: 'users', expectedRecord: { name: '王华', sex: '男', age: 21 }, keyword: 't1yOS',
  },
}

export default function AuthenticityHostedSettings({ embedded = false }: { embedded?: boolean }) {
  const qc = useQueryClient()
  const { data } = useQuery({ queryKey: ['authenticityConfig'], queryFn: api.authenticityConfig })
  const [hosted, setHosted] = useState<AuthenticityHostedConfig>(defaults)
  const [headersText, setHeadersText] = useState('{}')
  const [recordText, setRecordText] = useState('{}')
  const [importText, setImportText] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const s = data?.settings as AuthenticityHostedConfig | undefined
    if (!s) return
    const next: AuthenticityHostedConfig = {
      ...defaults,
      ...s,
      providers: { ...defaults.providers, ...(s.providers || {}) },
      mcp: { ...defaults.mcp, ...(s.mcp || {}) },
      proof: { ...defaults.proof, ...(s.proof || {}) },
      legacy: { ...defaults.legacy, ...(s.legacy || {}) },
    }
    setHosted(next)
    setHeadersText(JSON.stringify(next.mcp.headers || {}, null, 2))
    setRecordText(JSON.stringify(next.legacy.expectedRecord || {}, null, 2))
  }, [data])

  const importJSON = () => {
    let raw: any
    try {
      raw = JSON.parse(importText)
    } catch (e: any) {
      return message.error('JSON 解析失败: ' + (e?.message || ''))
    }
    const next: AuthenticityHostedConfig = {
      ...defaults,
      ...raw,
      providers: { ...defaults.providers, ...(raw.providers || {}) },
      mcp: { ...defaults.mcp, ...(raw.mcp || {}) },
      proof: { ...defaults.proof, ...(raw.proof || {}) },
      legacy: { ...defaults.legacy, ...(raw.legacy || {}) },
    }
    setHosted(next)
    setHeadersText(JSON.stringify(next.mcp.headers || {}, null, 2))
    setRecordText(JSON.stringify(next.legacy.expectedRecord || {}, null, 2))
    message.success('已导入到表单，检查后点击“保存工具调用配置”')
  }

  const save = async () => {
    let headers: Record<string, string> = {}
    let expectedRecord: Record<string, any> = {}
    try {
      headers = JSON.parse(headersText || '{}')
      if (Array.isArray(headers) || typeof headers !== 'object') throw new Error('headers 必须是 JSON 对象')
    } catch (e: any) {
      return message.error('MCP Headers 不是合法 JSON 对象: ' + (e?.message || ''))
    }
    try {
      expectedRecord = JSON.parse(recordText || '{}')
      if (Array.isArray(expectedRecord) || typeof expectedRecord !== 'object') throw new Error('expectedRecord 必须是 JSON 对象')
    } catch (e: any) {
      return message.error('Legacy expectedRecord 不是合法 JSON 对象: ' + (e?.message || ''))
    }
    setSaving(true)
    try {
      const payload: AuthenticityHostedConfig = {
        ...hosted,
        mcp: { ...hosted.mcp, headers },
        legacy: { ...hosted.legacy, expectedRecord },
      }
      await api.saveAuthenticityConfig(payload)
      message.success('工具调用配置已保存（凭据加密存储）')
      qc.invalidateQueries({ queryKey: ['authenticityConfig'] })
    } catch (e: any) {
      message.error(e?.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const body = (
    <div>
      {!embedded && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="统一配置，一次保存后新建真实性测试默认使用"
          description="这里保存的 MCP / 证明页配置会加密写入 SQLite；新建模型真实性测试时默认勾选“启用工具调用检测”，无需每次重新填写。"
        />
      )}
      <Collapse
        ghost
        style={{ marginBottom: 8 }}
        items={[{
          key: 'import',
          label: '从 JSON 导入配置（可粘贴完整 MCP / 证明页配置）',
          children: (
            <div>
              <Input.TextArea
                className="code-input"
                autoSize={{ minRows: 4, maxRows: 12 }}
                value={importText}
                onChange={e => setImportText(e.target.value)}
                placeholder={'{\n  "profile": "t1y-legacy",\n  "defaultEnabled": true,\n  "mcp": { "serverUrl": "...", "headers": { ... } },\n  "legacy": { "domain": "t1y.net" }\n}'}
              />
              <Button style={{ marginTop: 8 }} onClick={importJSON}>导入到表单</Button>
            </div>
          ),
        }]}
      />
      <Row gutter={[14, 6]}>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>默认启用</div>
          <Switch checked={!!hosted.defaultEnabled} onChange={v => setHosted(h => ({ ...h, defaultEnabled: v }))} />
        </Col>
        <Col xs={12} lg={5}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Profile</div>
          <Select
            value={hosted.profile}
            onChange={v => setHosted(h => ({ ...h, profile: v }))}
            style={{ width: '100%' }}
            options={[
              { value: 'generic', label: 'generic（随机 nonce）' },
              { value: 't1y-legacy', label: 't1y-legacy（兼容原契约）' },
              { value: 'custom', label: 'custom' },
            ]}
          />
        </Col>
        <Col xs={24} lg={15}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>适用协议（保存后作为新建测试的默认值）</div>
          <Space>
            {(['openai', 'claude', 'gemini'] as const).map(p => (
              <Tag.CheckableTag
                key={p}
                checked={!!hosted.providers?.[p]}
                onChange={checked => setHosted(h => ({ ...h, providers: { ...h.providers, [p]: checked } }))}
              >
                {p}
              </Tag.CheckableTag>
            ))}
          </Space>
        </Col>
      </Row>

      <div className="panel-title" style={{ marginTop: 14 }}>MCP</div>
      <Row gutter={[14, 6]}>
        <Col xs={24} lg={10}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>MCP Server URL</div>
          <Input value={hosted.mcp.serverUrl} onChange={e => setHosted(h => ({ ...h, mcp: { ...h.mcp, serverUrl: e.target.value } }))} placeholder="https://your-host/mcp" />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Server Label</div>
          <Input value={hosted.mcp.serverLabel} onChange={e => setHosted(h => ({ ...h, mcp: { ...h.mcp, serverLabel: e.target.value } }))} />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>MCP 工具名</div>
          <Input value={hosted.mcp.toolName} onChange={e => setHosted(h => ({ ...h, mcp: { ...h.mcp, toolName: e.target.value } }))} />
        </Col>
        <Col xs={24} lg={6}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>require_approval</div>
          <Input value={hosted.mcp.requireApproval} onChange={e => setHosted(h => ({ ...h, mcp: { ...h.mcp, requireApproval: e.target.value } }))} />
        </Col>
        <Col xs={24} lg={12}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Server Description</div>
          <Input value={hosted.mcp.serverDescription || ''} onChange={e => setHosted(h => ({ ...h, mcp: { ...h.mcp, serverDescription: e.target.value } }))} />
        </Col>
        <Col xs={24} lg={12}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>MCP Headers（JSON；值可用 **** 保留已保存的密钥）</div>
          <Input.TextArea
            className="code-input"
            autoSize={{ minRows: 4, maxRows: 10 }}
            value={headersText}
            onChange={e => setHeadersText(e.target.value)}
            placeholder={'{"X-T1Y-Application-ID":"1001","X-T1Y-API-Key":"..."}'}
          />
        </Col>
        <Col xs={24} lg={12}>
          <Alert
            type="warning"
            showIcon
            style={{ marginTop: 20 }}
            message="凭据会发送给被检测端点"
            description="MCP header 中的凭据对被检测端点可见；请只对可信上游启用，并使用只读、短时、可轮换的凭据。报告和导出会脱敏。"
          />
        </Col>
      </Row>

      <div className="panel-title" style={{ marginTop: 14 }}>证明页 / 旧契约</div>
      <Row gutter={[14, 6]}>
        <Col xs={24} lg={8}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>证明页 Base URL</div>
          <Input value={hosted.proof.baseUrl} onChange={e => setHosted(h => ({ ...h, proof: { ...h.proof, baseUrl: e.target.value } }))} placeholder="https://your-host" />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>证明域名</div>
          <Input value={hosted.proof.domain} onChange={e => setHosted(h => ({ ...h, proof: { ...h.proof, domain: e.target.value } }))} placeholder="proof.example.com" />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Path 模板</div>
          <Input value={hosted.proof.pathTemplate} onChange={e => setHosted(h => ({ ...h, proof: { ...h.proof, pathTemplate: e.target.value } }))} />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Legacy 域名</div>
          <Input value={hosted.legacy.domain} onChange={e => setHosted(h => ({ ...h, legacy: { ...h.legacy, domain: e.target.value } }))} placeholder="t1y.net" />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Legacy 环境变量</div>
          <Input value={hosted.legacy.envVar} onChange={e => setHosted(h => ({ ...h, legacy: { ...h.legacy, envVar: e.target.value } }))} />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>期望值</div>
          <Input value={hosted.legacy.expectedEnvValue} onChange={e => setHosted(h => ({ ...h, legacy: { ...h.legacy, expectedEnvValue: e.target.value } }))} />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>集合名</div>
          <Input value={hosted.legacy.collection} onChange={e => setHosted(h => ({ ...h, legacy: { ...h.legacy, collection: e.target.value } }))} />
        </Col>
        <Col xs={12} lg={4}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>关键字</div>
          <Input value={hosted.legacy.keyword} onChange={e => setHosted(h => ({ ...h, legacy: { ...h.legacy, keyword: e.target.value } }))} />
        </Col>
        <Col xs={24} lg={16}>
          <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>Legacy expectedRecord（JSON）</div>
          <Input.TextArea
            className="code-input"
            autoSize={{ minRows: 2, maxRows: 6 }}
            value={recordText}
            onChange={e => setRecordText(e.target.value)}
          />
        </Col>
      </Row>

      <Space style={{ marginTop: 14 }}>
        <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>保存工具调用配置</Button>
        <span className="muted" style={{ fontSize: 12 }}>
          当前配置：{hosted.mcp.serverUrl ? `MCP ${hosted.mcp.serverUrl}` : '未配置 MCP'} · {hosted.proof.domain || hosted.legacy.domain || '未配置证明域名'}
        </span>
      </Space>
    </div>
  )

  if (embedded) return body
  return <div className="panel"><div className="panel-title">模型真实性 / 工具调用</div>{body}</div>
}
