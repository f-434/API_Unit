import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { LeftOutlined, PlusOutlined, RightOutlined } from '@ant-design/icons'

export interface QuickAction {
  key: string
  label: string
  icon: ReactNode
  onClick: () => void
  disabled?: boolean
}

interface Pos { x: number; y: number }
type Dock = 'right' | null

const HANDLE_W = 28
const HANDLE_H = 64
const PANEL_W = 200
const GAP = 8
const EDGE = 24
const TOP_MIN = 56

function clampY(v: number): number {
  const max = Math.max(TOP_MIN, window.innerHeight - HANDLE_H - 12)
  const n = Number.isFinite(v) ? v : TOP_MIN
  return Math.min(Math.max(TOP_MIN, n), max)
}

function clampFree(p: Pos): Pos {
  const maxX = Math.max(0, window.innerWidth - HANDLE_W)
  return { x: Math.min(Math.max(0, p.x), maxX), y: clampY(p.y) }
}

interface Saved { x?: number; y?: number; dock?: Dock }

function readSaved(key: string): Saved | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const v = JSON.parse(raw)
    return v && typeof v === 'object' ? v : null
  } catch {
    return null
  }
}

function persist(key: string, value: Saved) {
  try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* ignore */ }
}

export default function QuickActionsFab({
  actions, storageKey = 'apiunit.quickfab',
}: {
  actions: QuickAction[]
  storageKey?: string
}) {
  const [dock, setDock] = useState<Dock>(() => {
    const s = readSaved(storageKey)
    return s?.dock === null ? null : 'right'
  })
  const [pos, setPos] = useState<Pos>(() => {
    const s = readSaved(storageKey)
    const y = clampY(s?.y ?? window.innerHeight - HANDLE_H - 120)
    if (s?.dock !== null) {
      return { x: Math.max(0, window.innerWidth - HANDLE_W), y }
    }
    return clampFree({ x: typeof s?.x === 'number' ? s.x : window.innerWidth - HANDLE_W, y })
  })
  const [expanded, setExpanded] = useState(false)
  const [dragging, setDragging] = useState(false)
  const collapseTimer = useRef<number>()
  const drag = useRef<{
    startX: number
    startY: number
    originX: number
    originY: number
    moved: boolean
    keepDock: boolean
    wasExpanded: boolean
  } | null>(null)

  const openMenu = () => {
    if (collapseTimer.current) window.clearTimeout(collapseTimer.current)
    setExpanded(true)
  }
  const scheduleClose = () => {
    if (collapseTimer.current) window.clearTimeout(collapseTimer.current)
    collapseTimer.current = window.setTimeout(() => setExpanded(false), 200)
  }

  useEffect(() => {
    const onResize = () => {
      if (dock === 'right') {
        setPos(p => ({ x: Math.max(0, window.innerWidth - HANDLE_W), y: clampY(p.y) }))
      } else {
        setPos(p => clampFree(p))
      }
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setExpanded(false)
    }
    window.addEventListener('resize', onResize)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('resize', onResize)
      window.removeEventListener('keydown', onKey)
      if (collapseTimer.current) window.clearTimeout(collapseTimer.current)
    }
  }, [dock])

  const onPointerDown = (e: ReactPointerEvent<HTMLButtonElement>) => {
    if (e.button !== 0) return
    const originX = dock === 'right' ? window.innerWidth - HANDLE_W : pos.x
    drag.current = {
      startX: e.clientX,
      startY: e.clientY,
      originX,
      originY: pos.y,
      moved: false,
      keepDock: dock === 'right',
      wasExpanded: expanded,
    }
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  const onPointerMove = (e: ReactPointerEvent<HTMLButtonElement>) => {
    const d = drag.current
    if (!d) return
    const dx = e.clientX - d.startX
    const dy = e.clientY - d.startY
    if (!d.moved && Math.hypot(dx, dy) < 5) return
    if (!d.moved) {
      d.moved = true
      setDragging(true)
    }
    if (d.keepDock && Math.abs(dx) <= 8) {
      setPos(p => ({ ...p, y: clampY(d.originY + dy) }))
      return
    }
    d.keepDock = false
    setDock(null)
    setPos(clampFree({ x: d.originX + dx, y: d.originY + dy }))
  }

  const onPointerUp = (e: ReactPointerEvent<HTMLButtonElement>) => {
    const d = drag.current
    drag.current = null
    if (!d) return
    setDragging(false)
    if (!d.moved) {
      if (e.pointerType === 'touch') setExpanded(!d.wasExpanded)
      else setExpanded(true)
      return
    }
    const dx = e.clientX - d.startX
    const dy = e.clientY - d.startY
    const y = clampY(d.originY + dy)
    if (d.keepDock) {
      const p = { x: Math.max(0, window.innerWidth - HANDLE_W), y }
      setDock('right')
      setPos(p)
      persist(storageKey, { ...p, dock: 'right' })
      return
    }
    const hx = d.originX + dx
    if (hx + HANDLE_W >= window.innerWidth - EDGE) {
      const p = { x: Math.max(0, window.innerWidth - HANDLE_W), y }
      setDock('right')
      setPos(p)
      persist(storageKey, { ...p, dock: 'right' })
      return
    }
    const p = clampFree({ x: hx, y })
    setDock(null)
    setPos(p)
    persist(storageKey, { ...p, dock: null })
  }

  const hover = {
    onMouseEnter: openMenu,
    onMouseLeave: scheduleClose,
    onFocus: openMenu,
    onBlur: scheduleClose,
  }

  const handle = (
    <button
      type="button"
      className="quick-fab-handle"
      aria-label="快捷操作"
      aria-expanded={expanded}
      title="快捷操作（可拖动）"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      {dock === 'right' ? (expanded ? <RightOutlined /> : <LeftOutlined />) : <PlusOutlined />}
    </button>
  )

  const panelOpenLeft = dock !== 'right' && pos.x + HANDLE_W + GAP + PANEL_W > window.innerWidth - 8
  const panelClass = `quick-fab-panel${panelOpenLeft ? ' quick-fab-panel--left' : ''}${expanded ? ' open' : ''}`

  const panel = (
    <div className={panelClass} role="menu" aria-label="快捷操作" aria-hidden={!expanded}>
      <div className="quick-fab-panel-title">快捷操作</div>
      {actions.map(a => (
        <button
          key={a.key}
          type="button"
          role="menuitem"
          className="quick-fab-item"
          tabIndex={expanded ? 0 : -1}
          disabled={a.disabled}
          onClick={() => { a.onClick(); setExpanded(false) }}
        >
          <span className="quick-fab-item-icon">{a.icon}</span>
          <span>{a.label}</span>
        </button>
      ))}
    </div>
  )

  const rootClass = `quick-fab-root ${dock === 'right' ? 'docked' : 'floating'}${dragging ? ' dragging' : ''}`
  const rootStyle = dock === 'right'
    ? { right: 0, top: pos.y }
    : { left: pos.x, top: pos.y }

  return (
    <div className={rootClass} style={rootStyle} {...hover}>
      {panel}
      {handle}
    </div>
  )
}
