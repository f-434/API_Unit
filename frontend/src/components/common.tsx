import React, { useState } from 'react'
import { Button, Empty, Space, Tag, Tooltip, Typography, message } from 'antd'
import { CopyOutlined, CheckOutlined } from '@ant-design/icons'
import type { Kind } from '../types'
import {
  kindColor, kindLabel, protocolLabel, severityColor, severityLabel, statusColor,
  statusLabel, verdictColor, verdictLabel,
} from '../format'

export function PageHeader({ title, subtitle, extra }: { title: React.ReactNode; subtitle?: React.ReactNode; extra?: React.ReactNode }) {
  return (
    <div className="page-header">
      <div>
        <h1>{title}</h1>
        {subtitle && <p>{subtitle}</p>}
      </div>
      {extra && <Space wrap>{extra}</Space>}
    </div>
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
export function VerdictTag({ verdict }: { verdict?: string }) {
  if (!verdict) return <span className="muted">-</span>
  return <Tag color={verdictColor(verdict)}>{verdictLabel(verdict)}</Tag>
}
export function SeverityTag({ severity }: { severity?: string }) {
  if (!severity) return <span className="muted">-</span>
  return <Tag color={severityColor(severity)}>{severityLabel(severity)}</Tag>
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
