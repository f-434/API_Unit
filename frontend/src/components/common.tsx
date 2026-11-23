import React, { useState } from 'react'
import { Button, Collapse, Empty, Space, Tag, Tooltip, Typography, message } from 'antd'
import { CopyOutlined, CheckOutlined } from '@ant-design/icons'
import type { Kind, TargetRef } from '../types'
import {
  kindColor, kindLabel, protocolLabel, severityColor, severityLabel, statusColor,
  statusLabel, verdictColor, verdictLabel,
} from '../format'

export function PageHeader({ title, extra }: { title: React.ReactNode; extra?: React.ReactNode }) {
  return (
    <div className="page-header">
      <h1>{title}</h1>
      {extra && <Space wrap>{extra}</Space>}
    </div>
  )
}

/** 页脚一行摘要：替代一排只读 Tag，保持可扫描且不占视觉重量。 */
export function SummaryLine({ items }: { items: { label: string; value: React.ReactNode }[] }) {
  return (
    <div className="summary-line">
      {items.map(it => (
        <span key={it.label} className="summary-item">{it.label} <b>{it.value}</b></span>
      ))}
    </div>
  )
}

/** 判定口径等长文本默认折叠，需要时再展开，避免每个页面常驻说明块。 */
export function NotesCollapse({ notes, title = '判定口径' }: { notes?: string[]; title?: string }) {
  if (!notes?.length) return null
  return (
    <Collapse
      ghost
      className="notes-collapse"
      items={[{
        key: 'notes',
        label: title,
        children: <ul className="notes-list">{notes.map((n, i) => <li key={i}>{n}</li>)}</ul>,
      }]}
    />
  )
}

export function Kpi({ label, value, suffix, color }: { label: string; value: React.ReactNode; suffix?: string; color?: string }) {
  return (
    <div className="kpi">
      <div className="label">{label}</div>
      <div className="value" style={color ? { color } : undefined}>
        {value}
        {suffix && <span className="suffix">{suffix}</span>}
      </div>
    </div>
  )
}

export function KindTag({ kind }: { kind?: Kind | string }) {
  return <Tag color={kindColor(kind)}>{kindLabel(kind)}</Tag>
}
export function StatusTag({ status }: { status?: string }) {
  return <Tag color={statusColor(status)}>{statusLabel(status)}</Tag>
}
export function ProtocolTag({ protocol }: { protocol?: string }) {
  const color = protocol === 'claude' ? 'orange' : protocol === 'gemini' ? 'purple' : protocol === 'openai' ? 'blue' : 'default'
  return <Tag color={color}>{protocolLabel(protocol)}</Tag>
}

/**
 * Shows the model name(s) the upstream reported in its response. When the
 * upstream maps the requested model to another one (e.g. Gemini modelVersion
 * or a gateway redirect) the tags turn orange and explain the mismatch.
 */
export function ResponseModelCell({ requested, models, max = 3 }: {
  requested?: string
  models?: (string | null | undefined)[]
  max?: number
}) {
  const list = Array.from(new Set((models || []).filter((m): m is string => !!m)))
  if (!list.length) return <span className="muted">-</span>
  const differs = !!requested && list.some(m => m !== requested)
  const shown = list.slice(0, max)
  return (
    <Space size={4} wrap>
      {shown.map(m => {
        const tag = <Tag key={m} color={differs ? 'orange' : undefined} className="small-mono">{m}</Tag>
        return differs ? (
          <Tooltip key={m} title={`上游实际返回 ${m}，与请求模型 ${requested} 不一致`}>{tag}</Tooltip>
        ) : tag
      })}
      {list.length > shown.length && (
        <Tooltip title={list.join('、')}><Tag>+{list.length - shown.length}</Tag></Tooltip>
      )}
    </Space>
  )
}

export function VerdictTag({ verdict }: { verdict?: string }) {
  if (!verdict) return <span className="muted">-</span>
  return <Tag color={verdictColor(verdict)}>{verdictLabel(verdict)}</Tag>
}
export function SeverityTag({ severity }: { severity?: string }) {
  if (!severity) return <span className="muted">-</span>
  return <Tag color={severityColor(severity)}>{severityLabel(severity)}</Tag>
}

export function TargetTags({ targets, max = 3 }: { targets?: TargetRef[]; max?: number }) {
  const names = Array.from(new Set((targets || []).map(t => t.name).filter(Boolean)))
  if (!names.length) return <span className="muted">-</span>
  const shown = names.slice(0, max)
  const rest = names.slice(max)
  return (
    <Space size={4} wrap>
      {shown.map(name => <Tag key={name}>{name}</Tag>)}
      {rest.length > 0 && (
        <Tooltip title={names.join('、')}>
          <Tag>+{rest.length}</Tag>
        </Tooltip>
      )}
    </Space>
  )
}

export function CopyButton({ text, label }: { text?: string; label?: string }) {
  const [done, setDone] = useState(false)
  const copy = async () => {
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      setDone(true)
      setTimeout(() => setDone(false), 1200)
    } catch {
      message.error('复制失败')
    }
  }
  return (
    <Button size="small" type="text" icon={done ? <CheckOutlined /> : <CopyOutlined />} onClick={copy}>
      {label || (done ? '已复制' : '复制')}
    </Button>
  )
}

/** Pretty-print plain JSON or an SSE stream (one JSON object per data: line). */
function prettyBody(raw: string): string {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2)
  } catch {
    // Fall through to SSE handling.
  }
  // The backend stores streaming payloads as newline-delimited JSON (without
  // the SSE "data:" prefix), and hand-written SSE bodies may keep the prefix.
  const lines = raw.split(/\r?\n/)
  const nonEmpty = lines.filter(l => l.trim() !== '').length
  let parsedCount = 0
  const out = lines.map(line => {
    const trimmedLine = line.trim()
    if (!trimmedLine) return line
    const m = /^data:\s*(.*)$/.exec(trimmedLine)
    const payload = m ? m[1] : trimmedLine
    if (!payload || payload === '[DONE]') return line
    try {
      const pretty = JSON.stringify(JSON.parse(payload), null, 2)
      parsedCount++
      return m ? 'data: ' + pretty : pretty
    } catch {
      return line
    }
  })
  if (parsedCount > 0 && parsedCount >= Math.max(1, nonEmpty * 0.5)) {
    return out.join('\n')
  }
  return raw
}

export function JsonBlock({ value, title, maxHeight = 420 }: { value: any; title?: string; maxHeight?: number }) {
  let text = ''
  if (typeof value === 'string') {
    text = prettyBody(value)
  } else {
    try { text = JSON.stringify(value, null, 2) } catch { text = String(value) }
  }
  return (
    <div className="json-block">
      {title ? (
        <div className="panel-title" style={{ marginBottom: 6 }}>{title}<CopyButton text={text} /></div>
      ) : (
        <div className="json-toolbar"><CopyButton text={text} /></div>
      )}
      <pre style={{ maxHeight }}>{text || '-'}</pre>
    </div>
  )
}

export function EmptyPanel({ text = '暂无数据' }: { text?: string }) {
  return (
    <div style={{ padding: 30 }}>
      <Empty description={<span className="muted">{text}</span>} />
    </div>
  )
}

export function LinkText({ children, href }: { children: React.ReactNode; href: string }) {
  return <Typography.Link href={href} target="_blank" rel="noopener">{children}</Typography.Link>
}

export function EllipsisText({ text, width = 260 }: { text?: string; width?: number }) {
  if (!text) return <span className="muted">-</span>
  return (
    <Tooltip title={<div style={{ whiteSpace: 'pre-wrap', maxWidth: 520 }}>{text}</div>}>
      <span style={{ display: 'inline-block', maxWidth: width, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom' }}>
        {text}
      </span>
    </Tooltip>
  )
}
