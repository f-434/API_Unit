import {
  Alert, Button, Col, Collapse, Descriptions, Image, Row, Space, Table, Tag, Tooltip, message,
} from 'antd'
import { DownloadOutlined, LinkOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import type {
  ImageDetectionImage, ImageDetectionLiveModel, ImageDetectionModelReport, ImageDetectionPhase,
  ImageDetectionReport, Live,
} from '../types'
import { EmptyPanel, JsonBlock, Kpi, NotesCollapse } from './common'

const statusColor: Record<string, string> = { pass: 'green', fail: 'red', warn: 'gold', na: 'default' }
const statusText: Record<string, string> = { pass: '通过', fail: '失败', warn: '警告', na: 'N/A' }
const sigColor: Record<string, string> = {
  detected: 'green', not_detected: 'default', possible: 'gold', unknown: 'default',
}
const sigText: Record<string, string> = {
  detected: '已检测到', not_detected: '未检测到', possible: '疑似', unknown: '未知',
}
const onlineStatusText: Record<string, string> = {
  checked: '已校验', unconfigured: '未配置 Key', error: '校验失败', skipped: '未启用',
}
const outcomeText: Record<string, string> = { detected: '检测到', not_detected: '未检测到' }
const validationText: Record<string, string> = {
  trusted: '受信任', valid: '有效', invalid: '无效', not_present: '不存在',
}

function absoluteImageUrl(path: string): string {
  try {
    return new URL(path, window.location.origin).toString()
  } catch {
    return path
  }
}

function isPrivateHost(): boolean {
  const host = window.location.hostname
  return host === 'localhost' || host === '127.0.0.1' || host === '::1' ||
    host.startsWith('10.') || host.startsWith('192.168.') ||
    /^172\.(1[6-9]|2\d|3[01])\./.test(host)
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('图片链接已复制')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

function downloadImage(img: ImageDetectionImage) {
  const a = document.createElement('a')
  a.href = img.path
  a.download = `image-${img.sha256.slice(0, 8)}.${img.format || 'png'}`
  document.body.appendChild(a)
  a.click()
  a.remove()
}

function SignatureBlock({ image }: { image: ImageDetectionImage }) {
  const sig = image.signature
  const online = sig.online
  const onlineChecked = online?.status === 'checked'

  const links = sig.links || []
  const adobe = links.find(l => l.key === 'adobe')
  const openai = links.find(l => l.key === 'openai')
  const azure = links.find(l => l.key === 'azure')

  const publicImage = !isPrivateHost()
  const localUrl = absoluteImageUrl(image.path)
  const sourceUrl = image.upstreamUrl && /^https?:\/\//i.test(image.upstreamUrl)
    ? image.upstreamUrl
    : (publicImage ? localUrl : '')
  const adobeUrl = sourceUrl
    ? `https://inspect.cr?source=${encodeURIComponent(sourceUrl)}`
    : (adobe?.autoUrl || adobe?.url || 'https://contentauthenticity.adobe.com/inspect')

  const c2paDetected = sig.c2pa.status === 'detected' ||
    (onlineChecked && online?.c2pa.outcome === 'detected')
  const synthDetected = onlineChecked && online?.synthId.outcome === 'detected'
  const signatureBadge = c2paDetected && synthDetected
    ? { color: 'green', text: 'C2PA + SynthID 均已检测到' }
    : c2paDetected
      ? { color: 'gold', text: onlineChecked ? '仅检测到 C2PA' : '检测到 C2PA；SynthID 需在线验证' }
      : synthDetected
        ? { color: 'gold', text: '仅检测到 SynthID' }
        : { color: 'default', text: onlineChecked ? '未检测到签名信号' : '签名未确认（需在线验证）' }

  const c2paOnline = !online ? (
    <Tag>未检查</Tag>
  ) : !onlineChecked ? (
    <Tag color={online.status === 'error' ? 'red' : 'gold'}>{onlineStatusText[online.status] || online.status}</Tag>
  ) : (
    <Space size={4} wrap>
      <Tag color={online.c2pa.outcome === 'detected' ? 'green' : 'default'}>
        {outcomeText[online.c2pa.outcome || ''] || online.c2pa.outcome || '无结果'}
      </Tag>
      {online.c2pa.validationState && <Tag>验签 {validationText[online.c2pa.validationState] || online.c2pa.validationState}</Tag>}
      {online.c2pa.issuer && <span className="muted">{online.c2pa.issuer}</span>}
      {online.c2pa.model && <span className="small-mono muted">{online.c2pa.model}</span>}
      {online.c2pa.generatedAt && <span className="muted">{online.c2pa.generatedAt}</span>}
    </Space>
  )

  const synthOnline = !online ? (
    <Tag>未检查</Tag>
  ) : !onlineChecked ? (
    <Tag color={online.status === 'error' ? 'red' : 'gold'}>{onlineStatusText[online.status] || online.status}</Tag>
  ) : (
    <Space size={4} wrap>
      <Tag color={online.synthId.outcome === 'detected' ? 'green' : 'default'}>
        {outcomeText[online.synthId.outcome || ''] || online.synthId.outcome || '无结果'}
      </Tag>
      {online.synthId.model && <span className="small-mono muted">{online.synthId.model}</span>}
      {online.synthId.generatedAt && <span className="muted">{online.synthId.generatedAt}</span>}
    </Space>
  )

  return (
    <div className="case-card" style={{ marginTop: 10 }}>
      <div className="panel-title" style={{ fontSize: 13, marginBottom: 8 }}>
        <SafetyCertificateOutlined /> 来源签名（C2PA / SynthID）
        <Tag color={signatureBadge.color} style={{ fontWeight: 400 }}>{signatureBadge.text}</Tag>
      </div>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="C2PA · 本地">
          <Space direction="vertical" size={2} style={{ width: '100%' }}>
            <Tag color={sigColor[sig.c2pa.status] || 'default'}>{sigText[sig.c2pa.status] || sig.c2pa.status}</Tag>
            {!!sig.c2pa.evidence?.length && <span className="small-mono muted">{sig.c2pa.evidence.join('；')}</span>}
          </Space>
        </Descriptions.Item>
        <Descriptions.Item label="SynthID · 本地">
          <Space direction="vertical" size={2} style={{ width: '100%' }}>
            <Tag color={sigColor[sig.synthId.status] || 'default'}>{sigText[sig.synthId.status] || sig.synthId.status}</Tag>
            {!!sig.synthId.evidence?.length && <span className="small-mono muted">{sig.synthId.evidence.join('；')}</span>}
          </Space>
        </Descriptions.Item>
        <Descriptions.Item label="C2PA · 在线">{c2paOnline}</Descriptions.Item>
        <Descriptions.Item label="SynthID · 在线">{synthOnline}</Descriptions.Item>
      </Descriptions>

      {online?.status === 'unconfigured' && (
        <Alert
          style={{ marginTop: 8 }}
          type="warning"
          showIcon
          message="在线验签未配置"
          description="可在「生图检测」页面底部保存 OpenAI API Key 后自动同时校验 C2PA 与 SynthID；也可以使用下方按钮手动上传图片。"
        />
      )}
      {online?.status === 'error' && (
        <Alert style={{ marginTop: 8 }} type="error" showIcon message="在线验签失败" description={online.error || '未知错误'} />
      )}

      <Space wrap style={{ marginTop: 10 }}>
        <Tooltip title={sourceUrl ? '通过图片 URL 自动传给 Adobe Inspect' : '当前图片地址不可公网访问，请在页面中手动上传'}>
          <Button size="small" icon={<LinkOutlined />} href={adobeUrl} target="_blank" rel="noopener">
            Adobe Inspect
          </Button>
        </Tooltip>
        <Button size="small" icon={<LinkOutlined />} href={openai?.url || 'https://openai.com/zh-Hans-CN/research/verify/'} target="_blank" rel="noopener">
          OpenAI 验证
        </Button>
        <Button size="small" icon={<LinkOutlined />} href={azure?.url || 'https://ai.azure.com/nextgen/validate'} target="_blank" rel="noopener">
          Azure 验证
        </Button>
        <Button size="small" icon={<DownloadOutlined />} onClick={() => downloadImage(image)}>下载图片</Button>
        <Button size="small" onClick={() => copyText(localUrl)}>复制图片链接</Button>
        {!sourceUrl && <span className="muted" style={{ fontSize: 12 }}>当前站点为内网地址，Adobe 自动校验不可用，请下载后手动上传。</span>}
      </Space>

      {online?.raw && (
        <Collapse
          size="small"
          style={{ marginTop: 10 }}
          items={[{ key: 'raw', label: '在线验签原始响应', children: <JsonBlock value={online.raw} maxHeight={240} /> }]}
        />
      )}
    </div>
  )
}

function PhaseBlock({ phase }: { phase: ImageDetectionPhase }) {
  return (
    <div className="case-card" style={{ marginTop: 10 }}>
      <Space wrap style={{ marginBottom: 8 }}>
        <Tag color={statusColor[phase.status] || 'default'}>{statusText[phase.status] || phase.status}</Tag>
        <b>{phase.name}</b>
        <span className="small-mono muted">{phase.id}</span>
        {!!phase.statusCode && <Tag>HTTP {phase.statusCode}</Tag>}
        {phase.responseModel && <Tag color="blue">响应模型 {phase.responseModel}</Tag>}
        <span className="muted">{phase.durationMs} ms</span>
        {phase.requestId && <span className="small-mono muted">req {phase.requestId}</span>}
      </Space>
      {phase.reason && <div className="muted" style={{ marginBottom: 8 }}>{phase.reason}</div>}
      <Row gutter={[14, 10]}>
        <Col xs={24} lg={8}>
          {phase.image
            ? <Image src={phase.image.path} width="100%" style={{ borderRadius: 8, border: '1px solid #e5e6eb' }} />
            : <EmptyPanel text="本次没有生成图片" />}
          {phase.image && (
            <div className="muted" style={{ fontSize: 12, marginTop: 6, wordBreak: 'break-all' }}>
              <div>{phase.image.width}×{phase.image.height} · {phase.image.format} · {(phase.image.bytes / 1024).toFixed(1)} KB</div>
              <div className="small-mono">SHA-256 {phase.image.sha256.slice(0, 24)}…</div>
            </div>
          )}
        </Col>
        <Col xs={24} lg={16}>
          <Table
            size="small"
            rowKey={(_, i) => String(i)}
            pagination={false}
            dataSource={phase.checks || []}
            columns={[
              { title: '检查项', dataIndex: 'name', width: 130 },
              { title: '结果', dataIndex: 'status', width: 84, render: (v: string) => <Tag color={statusColor[v] || 'default'}>{statusText[v] || v}</Tag> },
              { title: '详情', dataIndex: 'detail', render: (v: string) => <span className="small-mono">{v || '-'}</span> },
            ]}
          />
          {phase.image && <SignatureBlock image={phase.image} />}
        </Col>
      </Row>
      <Collapse
        size="small"
        style={{ marginTop: 10 }}
        items={[
          {
            key: 'req', label: '请求 JSON',
            children: phase.requestJson ? <JsonBlock value={phase.requestJson} maxHeight={240} /> : <span className="muted">未保存</span>,
          },
          {
            key: 'resp', label: '响应 JSON（图片 base64 已脱敏）',
            children: phase.responseJson ? <JsonBlock value={phase.responseJson} maxHeight={300} /> : <span className="muted">未保存</span>,
          },
        ]}
      />
    </div>
  )
}

export default function ImageDetectionReportView({
  report, runId, live,
}: {
  report?: ImageDetectionReport
  runId: string
  live?: Live
}) {
  void runId

  if (!report || !report.summary) {
    const done = Number(live?.extra?.imageDetectionDone || 0)
    const total = Number(live?.extra?.imageDetectionTotal || 0)
    const models = (live?.extra?.imageDetectionLive || []) as ImageDetectionLiveModel[]
    const running = models.filter(m => !m.done).length
    return (
      <div>
        <div className="kpi-grid" style={{ marginBottom: 14 }}>
          <Kpi label="已完成测试项 / 总数" value={`${done} / ${total || '-'}`} />
          <Kpi label="进行中模型" value={running} color={running ? '#c026d3' : undefined} />
          <Kpi label="模型数" value={models.length} />
          <Kpi label="耗时" value={live ? `${(live.elapsedMs / 1000).toFixed(0)}s` : '-'} />
          <Kpi label="当前模型" value={live?.currentModel || '-'} />
        </div>
        {live?.extra?.imageDetectionSettingsError && (
          <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="在线验签配置读取失败" description={String(live.extra.imageDetectionSettingsError)} />
        )}
        {models.length === 0 ? <EmptyPanel text="生图检测运行中，等待第一批模型数据…" /> : (
          <Table
            rowKey={m => `${m.targetId}:${m.model}`}
            size="small"
            dataSource={models}
            pagination={false}
            scroll={{ x: 1080 }}
            columns={[
              { title: '上游', dataIndex: 'targetName', width: 160 },
              { title: '模型', dataIndex: 'model', width: 220, render: (v: string) => <span className="mono">{v}</span> },
              { title: '类型', dataIndex: 'kind', width: 90, render: (v: string) => <Tag>{v}</Tag> },
              { title: '尺寸', dataIndex: 'size', width: 110 },
              { title: '当前环节', dataIndex: 'stage', width: 230 },
              {
                title: '通过 / 失败 / N/A', width: 160,
                render: (_: any, m: ImageDetectionLiveModel) => (
                  <span><b style={{ color: '#16a34a' }}>{m.passed}</b> / <b style={{ color: m.failed ? '#dc2626' : undefined }}>{m.failed}</b> / {m.na}</span>
                ),
              },
              {
                title: '状态', width: 110,
                render: (_: any, m: ImageDetectionLiveModel) => m.done
                  ? <Tag color={m.failed ? 'red' : 'green'}>完成</Tag>
                  : <Tag color="processing">运行中</Tag>,
              },
              {
                title: '耗时', dataIndex: 'elapsedMs', width: 90,
                render: (v: number, m: ImageDetectionLiveModel) => {
                  // 运行中 extra 只在阶段切换时发布；这里按开始时间实时计算，
                  // 避免长请求期间一直显示 0.0s。
                  const started = Number(m.startedAt || 0)
                  const ms = m.done || !started ? (v || 0) : Math.max(v || 0, Date.now() - started)
                  return `${(ms / 1000).toFixed(1)}s`
                },
              },
              { title: '错误', dataIndex: 'error', ellipsis: true, render: (v: string) => v ? <span className="small-mono" style={{ color: '#dc2626' }}>{v}</span> : '-' },
            ]}
          />
        )}
      </div>
    )
  }

  const rows = (report.models || []) as ImageDetectionModelReport[]

  return (
    <div>
      <Table
        rowKey={m => `${m.targetId}:${m.model}`}
        size="small"
        dataSource={rows}
        pagination={false}
        scroll={{ x: 1100 }}
        expandable={{
          expandedRowRender: m => (
            <div style={{ padding: '4px 0 10px' }}>
              {m.error && <Alert type="error" showIcon style={{ marginBottom: 10 }} message="执行错误" description={m.error} />}
              {m.phases.length === 0 ? <EmptyPanel text="没有测试项" /> : m.phases.map(p => <PhaseBlock key={p.id + p.name} phase={p} />)}
            </div>
          ),
        }}
        columns={[
          { title: '上游', dataIndex: 'targetName', width: 160 },
          { title: '模型', dataIndex: 'model', width: 220, render: (v: string) => <span className="mono">{v}</span> },
          {
            title: '类型 / 尺寸', width: 180,
            render: (_: any, m: ImageDetectionModelReport) => <Space size={4}><Tag>{m.kind}</Tag><span className="small-mono">{m.size}</span></Space>,
          },
          {
            title: '通过 / 失败 / N/A', width: 160,
            render: (_: any, m: ImageDetectionModelReport) => (
              <span><b style={{ color: '#16a34a' }}>{m.passed}</b> / <b style={{ color: m.failed ? '#dc2626' : undefined }}>{m.failed}</b> / {m.na}</span>
            ),
          },
          {
            title: '通过率', width: 100,
            render: (_: any, m: ImageDetectionModelReport) => {
              const denom = m.passed + m.failed
              const rate = denom > 0 ? Math.round(m.passed / denom * 100) : 0
              return <span style={{ color: rate >= 100 ? '#16a34a' : rate >= 60 ? '#d97706' : '#dc2626' }}>{rate}%</span>
            },
          },
          { title: '耗时', dataIndex: 'durationMs', width: 90, render: (v: number) => `${((v || 0) / 1000).toFixed(1)}s` },
          {
            title: '错误 / 失败原因', ellipsis: true,
            render: (_: any, m: ImageDetectionModelReport) => {
              const failed = m.phases.find(p => p.status === 'fail')
              const text = m.error || failed?.reason || '-'
              return <Tooltip title={text}><span className="small-mono" style={{ color: text !== '-' ? '#dc2626' : undefined }}>{text}</span></Tooltip>
            },
          },
        ]}
      />

      <NotesCollapse notes={report.notes} />
    </div>
  )
}
