import ScrollTable from './ScrollTable'
import { Alert, Collapse, Descriptions, Space, Tag, Tooltip } from 'antd'
import type {
  Live, SignatureCheck, SignatureInfo, SignatureReport, SignatureRequest, SignatureVerdict,
} from '../types'
import { fmtCost, fmtDuration, fmtInt, fmtMs, fmtShortId, fmtTime } from '../format'
import { CopyButton, EmptyPanel, Kpi, NotesCollapse } from './common'

const verdictColor: Record<SignatureVerdict, string> = {
  pass: 'green',
  suspicious: 'gold',
  inconclusive: 'default',
  error: 'red',
}

const verdictText: Record<SignatureVerdict, string> = {
  pass: '通过',
  suspicious: '可疑',
  inconclusive: '证据不足',
  error: '执行错误',
}

const checkColor: Record<string, string> = { pass: 'green', fail: 'red', warn: 'gold', na: 'default' }
const checkText: Record<string, string> = { pass: '通过', fail: '失败', warn: '警告', na: 'N/A' }
const layoutText: Record<string, string> = { full: 'full', minimal: 'minimal', unknown: 'unknown', opaque: 'opaque' }
const sourceText: Record<string, string> = { meta: '字段', nested: '嵌套字段', search: '全树搜索', field: '字段', metaAny: '元数据节点' }

function VerdictTag({ verdict }: { verdict: SignatureVerdict }) {
  return <Tag color={verdictColor[verdict] || 'default'}>{verdictText[verdict] || verdict}</Tag>
}

function CheckTag({ check }: { check: SignatureCheck }) {
  return <Tag color={checkColor[check.status] || 'default'}>{checkText[check.status] || check.status}</Tag>
}

function RequestId({ value }: { value?: string }) {
  if (!value) return <span className="muted">-</span>
  return (
    <Tooltip title={value}>
      <span className="small-mono" style={{ display: 'inline-block', maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom' }}>
        {value}
      </span>
    </Tooltip>
  )
}

function blockSummary(req: SignatureRequest) {
  const counts = new Map<string, number>()
  for (const b of req.blocks || []) counts.set(b.type, (counts.get(b.type) || 0) + 1)
  if (!counts.size) return <span className="muted">-</span>
  return (
    <Space size={4} wrap>
      {Array.from(counts.entries()).map(([type, n]) => (
        <Tag key={type} color={type === 'thinking' ? 'geekblue' : type === 'redacted_thinking' ? 'purple' : undefined}>
          {type}{n > 1 ? `×${n}` : ''}
        </Tag>
      ))}
    </Space>
  )
}

function parsedSummary(req: SignatureRequest) {
  const parsed = (req.signatures || []).filter(s => !s.opaque && !s.parseError)
  if (!parsed.length) {
    if ((req.signatures || []).some(s => s.opaque)) return <Tag color="purple">redacted（不透明）</Tag>
    return <span className="muted">-</span>
  }
  return (
    <Space size={4} wrap>
      {parsed.slice(0, 2).map((s, i) => (
        <Space key={i} size={4} wrap>
          <Tag color={s.layout === 'full' ? 'green' : s.layout === 'minimal' ? 'gold' : 'default'}>
            {layoutText[s.layout] || s.layout}
          </Tag>
          {s.embeddedBlockType && <Tag>{s.embeddedBlockType}</Tag>}
          {s.model && <span className="small-mono">{s.model}</span>}
        </Space>
      ))}
      {parsed.length > 2 && <Tag>+{parsed.length - 2}</Tag>}
    </Space>
  )
}

function SignatureDetails({ sig, index }: { sig: SignatureInfo; index: number }) {
  return (
    <div className="case-card" style={{ marginTop: index ? 10 : 0 }}>
      <Space wrap style={{ marginBottom: 8 }}>
        <b>签名 {index + 1}</b>
        <Tag>{sig.blockType || '-'}</Tag>
        <Tag color={sig.layout === 'full' ? 'green' : sig.layout === 'minimal' ? 'gold' : sig.layout === 'opaque' ? 'purple' : 'default'}>
          {layoutText[sig.layout] || sig.layout || '未知'}
        </Tag>
        {sig.opaque && <Tag color="purple">不可解析（按设计不透明）</Tag>}
        {sig.parseError && <Tag color="red">解析失败</Tag>}
        <span className="muted">{fmtInt(sig.signatureChars)} 字符 / {fmtInt(sig.protoBytes)} 字节</span>
      </Space>
      {sig.parseError ? (
        <Alert type="error" showIcon message="签名解析失败" description={sig.parseError} />
      ) : sig.opaque ? null : (
        <>
          <Descriptions size="small" bordered column={2} style={{ marginBottom: 8 }}>
            <Descriptions.Item label="内嵌模型">
              {sig.model ? <span className="small-mono">{sig.model}</span> : <span className="muted">-</span>}
              {sig.modelSource && <Tag style={{ marginLeft: 6 }}>{sourceText[sig.modelSource] || sig.modelSource}</Tag>}
            </Descriptions.Item>
            <Descriptions.Item label="内嵌块类型">
              {sig.embeddedBlockType ? <Tag>{sig.embeddedBlockType}</Tag> : <span className="muted">-</span>}
              {sig.blockTypeSource && <Tag style={{ marginLeft: 6 }}>{sourceText[sig.blockTypeSource] || sig.blockTypeSource}</Tag>}
            </Descriptions.Item>
            <Descriptions.Item label="UUID">
              {sig.uuid ? <span className="small-mono">{sig.uuid}</span> : <span className="muted">-</span>}
            </Descriptions.Item>
            <Descriptions.Item label="签名时间（UTC+8）">
              {sig.signedAtUtc8 || <span className="muted">-</span>}
              {sig.timestampSource && <Tag style={{ marginLeft: 6 }}>{sourceText[sig.timestampSource] || sig.timestampSource}</Tag>}
            </Descriptions.Item>
            <Descriptions.Item label="元数据路径">{sig.metaPath || '<root>'}</Descriptions.Item>
            <Descriptions.Item label="元数据字段">
              <span className="small-mono">{sig.metaFields?.join(', ') || '-'}</span>
            </Descriptions.Item>
            <Descriptions.Item label="字段总数">{fmtInt(sig.totalFields)}</Descriptions.Item>
            <Descriptions.Item label="SHA-256">
              <span className="small-mono">{fmtShortId(sig.signatureSha256)}</span>
            </Descriptions.Item>
          </Descriptions>
          {!!sig.warnings?.length && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 8 }}
              message={`解析警告 ${sig.warnings.length} 条`}
              description={sig.warnings.slice(0, 5).join('；')}
            />
          )}
          <Collapse
            size="small"
            items={[
              { key: 'tree', label: 'Protobuf 结构', children: <pre className="json-block" style={{ maxHeight: 320, whiteSpace: 'pre-wrap', padding: 12, margin: 0 }}>{sig.tree || '-'}</pre> },
              { key: 'prefix', label: '签名前缀', children: <div><CopyButton text={sig.signaturePrefix} /><pre className="json-block" style={{ whiteSpace: 'pre-wrap', padding: 12, margin: 0, wordBreak: 'break-all' }}>{sig.signaturePrefix || '-'}</pre></div> },
            ]}
          />
        </>
      )}
    </div>
  )
}

function RequestDetail({ req }: { req: SignatureRequest }) {
  const usage = req.usage || { inputTokens: 0, outputTokens: 0, totalTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 }
  return (
    // 展开行位于横向可滚动的宽表内，限制宽度避免描述列表被撑到 1900px 后裁切。
    <div style={{ maxWidth: 'calc(100vw - 340px)' }}>
      <Descriptions size="small" bordered column={2} style={{ marginBottom: 12 }}>
        <Descriptions.Item label="请求 ID" span={2}>
          <Space size={6}>
            <span className="small-mono">{req.requestId || '-'}</span>
            {req.requestId && <CopyButton text={req.requestId} />}
          </Space>
        </Descriptions.Item>
        <Descriptions.Item label="响应 ID"><span className="small-mono">{req.responseId || '-'}</span></Descriptions.Item>
        <Descriptions.Item label="HTTP / 延迟">
          <Tag color={req.httpStatus >= 200 && req.httpStatus < 300 ? 'green' : 'red'}>{req.httpStatus || '无响应'}</Tag>
          {fmtMs(req.latencyMs)}
        </Descriptions.Item>
        <Descriptions.Item label="请求时间">{fmtTime(req.requestTime)}</Descriptions.Item>
        <Descriptions.Item label="尝试次数">{req.attempts}</Descriptions.Item>
        <Descriptions.Item label="响应模型">
          {req.responseModel ? <span className="small-mono">{req.responseModel}</span> : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="结束原因">{req.stopReason || '-'}</Descriptions.Item>
        <Descriptions.Item label="输入 / 输出 Token">{fmtInt(usage.inputTokens)} / {fmtInt(usage.outputTokens)}</Descriptions.Item>
        <Descriptions.Item label="缓存读 / 写">{fmtInt(usage.cacheReadTokens)} / {fmtInt(usage.cacheWriteTokens)}</Descriptions.Item>
        <Descriptions.Item label="成本">{fmtCost(req.cost)}</Descriptions.Item>
        <Descriptions.Item label="目标">{req.targetName || '-'}</Descriptions.Item>
      </Descriptions>

      {req.error && <Alert type="error" showIcon style={{ marginBottom: 12 }} message={req.error} />}
      {req.duplicate && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="签名与另一条请求完全相同"
          description="不同提示词返回同一个签名，通常意味着缓存回放或固定响应。"
        />
      )}

      <div className="panel-title" style={{ marginBottom: 8 }}>检查项</div>
      <ScrollTable
        size="small"
        rowKey="id"
        pagination={false}
        dataSource={req.checks || []}
        columns={[
          { title: '检查', dataIndex: 'name', width: 150 },
          { title: '结果', dataIndex: 'status', width: 90, render: (v: string, c: SignatureCheck) => <CheckTag check={c} /> },
          { title: '证据', dataIndex: 'evidence', render: (v: string) => <span className="small-mono">{v || '-'}</span> },
        ]}
      />

      <div className="panel-title" style={{ margin: '14px 0 8px' }}>签名解析</div>
      {(req.signatures || []).length === 0
        ? <EmptyPanel text="本次没有签名块" />
        : (req.signatures || []).map((s, i) => <SignatureDetails key={i} sig={s} index={i} />)}
    </div>
  )
}

function Running({ live }: { live?: Live }) {
  const extra = live?.extra || {}
  const total = Number(extra.claudeSignatureTotal || live?.planned || 0)
  const done = Number(extra.claudeSignatureDone || live?.completed || 0)
  return (
    <>
      <div className="kpi-grid" style={{ marginBottom: 14 }}>
        <Kpi label="进度" value={`${done} / ${total}`} />
        <Kpi label="当前模型" value={live?.currentModel || '-'} />
      </div>
      <EmptyPanel text="测试运行中，完成后显示逐条签名解析结果。" />
    </>
  )
}

export default function SignatureReportView({ report, live }: { report?: SignatureReport; live?: Live }) {
  if (!report?.summary) return <Running live={live} />
  const s = report.summary
  const models = report.models || []
  const requests = report.requests || []

  return (
    <>
      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">
          模型汇总
          <span className="muted" style={{ fontSize: 12, fontWeight: 400, marginLeft: 10 }}>
            full {s.full} · minimal {s.minimal} · unknown {s.unknown} · opaque {s.opaque}
          </span>
        </div>
        <ScrollTable
          size="small"
          rowKey={m => `${m.targetId}:${m.model}`}
          pagination={false}
          dataSource={models}
          scroll={{ x: 1180 }}
          columns={[
            { title: '上游', dataIndex: 'targetName', width: 160, ellipsis: true },
            { title: '模型', dataIndex: 'model', width: 200, render: (v: string) => <span className="mono">{v}</span> },
            { title: '请求', dataIndex: 'requests', width: 70 },
            { title: '通过', dataIndex: 'passed', width: 70, render: (v: number) => <span style={{ color: v ? '#16a34a' : undefined }}>{v}</span> },
            { title: '可疑', dataIndex: 'suspicious', width: 70, render: (v: number) => <span style={{ color: v ? '#d97706' : undefined }}>{v}</span> },
            { title: '证据不足', dataIndex: 'inconclusive', width: 90 },
            { title: '错误', dataIndex: 'errors', width: 70, render: (v: number) => <span style={{ color: v ? '#dc2626' : undefined }}>{v}</span> },
            { title: '签名 / 解析', width: 105, render: (_: any, m: any) => `${m.signatures} / ${m.parsed}` },
            { title: '重复', dataIndex: 'duplicates', width: 70, render: (v: number) => <span style={{ color: v ? '#d97706' : undefined }}>{v}</span> },
            { title: '成本', dataIndex: 'cost', width: 95, render: (v: number) => fmtCost(v) },
            { title: '耗时', dataIndex: 'durationMs', width: 90, render: (v: number) => fmtDuration(v) },
            { title: '错误', dataIndex: 'error', ellipsis: true, render: (v: string) => v ? <span style={{ color: '#dc2626' }}>{v}</span> : '-' },
          ]}
        />
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-title">逐条结果</div>
        <ScrollTable
          size="small"
          rowKey={r => String(r.globalIndex)}
          dataSource={requests}
          scroll={{ x: 1900 }}
          pagination={{ pageSize: 20, showSizeChanger: true }}
          expandable={{
            expandedRowRender: (r: SignatureRequest) => <RequestDetail req={r} />,
            rowExpandable: () => true,
          }}
          columns={[
            { title: '#', dataIndex: 'globalIndex', width: 58, render: (v: number) => <span className="muted">{v}</span> },
            { title: '目标', dataIndex: 'targetName', width: 130, ellipsis: true },
            { title: '模型', dataIndex: 'model', width: 190, ellipsis: true, render: (v: string) => <span className="mono">{v}</span> },
            {
              title: <Tooltip title="优先 X-Oneapi-Request-Id，缺失时回退 X-Request-Id / Request-Id 等响应头">请求 ID（oneapi-request-id）</Tooltip>,
              dataIndex: 'requestId', width: 210,
              render: (v: string) => <RequestId value={v} />,
            },
            {
              title: '判定', dataIndex: 'verdict', width: 105,
              render: (v: SignatureVerdict, r: SignatureRequest) => (
                <Space size={4} wrap>
                  <VerdictTag verdict={v} />
                  {r.duplicate && <Tag color="gold">重复</Tag>}
                </Space>
              ),
            },
            { title: '响应块', key: 'blocks', width: 170, render: (_: any, r: SignatureRequest) => blockSummary(r) },
            { title: '解析内容', key: 'parsed', width: 300, render: (_: any, r: SignatureRequest) => parsedSummary(r) },
            {
              title: 'UUID', key: 'uuid', width: 130,
              render: (_: any, r: SignatureRequest) => {
                const u = (r.signatures || []).find(s => s.uuid)?.uuid
                return u ? <span className="small-mono" title={u}>{fmtShortId(u)}</span> : <span className="muted">-</span>
              },
            },
            {
              title: '签名时间（UTC+8）', key: 'signedAt', width: 155,
              render: (_: any, r: SignatureRequest) => {
                const v = (r.signatures || []).find(s => s.signedAtUtc8)?.signedAtUtc8
                return v ? <span className="muted">{v}</span> : <span className="muted">-</span>
              },
            },
            {
              title: '签名大小', key: 'size', width: 120,
              render: (_: any, r: SignatureRequest) => {
                const parsed = (r.signatures || []).filter(s => !s.opaque && !s.parseError)
                if (!parsed.length) return <span className="muted">-</span>
                return <span className="muted">{fmtInt(parsed.reduce((n, s) => n + s.signatureChars, 0))} 字符</span>
              },
            },
            {
              title: 'HTTP / 延迟', key: 'http', width: 120,
              render: (_: any, r: SignatureRequest) => (
                <Space size={4}>
                  <Tag color={r.httpStatus >= 200 && r.httpStatus < 300 ? 'green' : 'red'}>{r.httpStatus || '无响应'}</Tag>
                  <span className="muted">{fmtMs(r.latencyMs)}</span>
                </Space>
              ),
            },
            {
              title: '错误 / 证据', key: 'error', width: 260, ellipsis: { showTitle: false },
              render: (_: any, r: SignatureRequest) => {
                const checks = r.checks || []
                const failed = checks.filter(c => c.status === 'fail')
                // 没有失败项时展示警告或“证据不足”的原因，避免空行看不出所以然。
                const notable = failed.length ? failed : checks.filter(c => c.status === 'warn' || (c.status === 'na' && c.evidence))
                const text = r.error || notable.map(c => `${c.name}: ${c.evidence}`).join('；')
                if (!text) return <span className="muted">-</span>
                return (
                  <Tooltip title={<div style={{ whiteSpace: 'pre-wrap' }}>{text}</div>}>
                    <span style={{ color: r.verdict === 'error' ? '#dc2626' : '#d97706' }}>{text}</span>
                  </Tooltip>
                )
              },
            },
          ]}
        />
      </div>

      <NotesCollapse notes={report.notes} title="解析口径与边界" />
    </>
  )
}
