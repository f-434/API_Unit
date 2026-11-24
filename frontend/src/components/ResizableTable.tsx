import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import type { TableProps } from 'antd'
import ScrollTable from './ScrollTable'

interface ResizableTableProps<T extends object> extends TableProps<T> {
  /** 传入后列宽会写入 localStorage；切换键时请同时更换组件 key。 */
  storageKey?: string
  /** 允许拖拽到的最小列宽。 */
  minWidth?: number
}

type Widths = Record<string, number>

function columnKey(col: any, index: number): string {
  if (col.key != null) return `key:${col.key}`
  if (col.dataIndex != null) {
    return `data:${Array.isArray(col.dataIndex) ? col.dataIndex.join('.') : col.dataIndex}`
  }
  if (typeof col.title === 'string' && col.title) return `title:${col.title}`
  return `col:${index}`
}

function loadWidths(key?: string): Widths {
  if (!key) return {}
  try {
    const parsed = JSON.parse(localStorage.getItem(key) || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: Widths = {}
    for (const [k, v] of Object.entries(parsed)) {
      if (typeof v === 'number' && Number.isFinite(v) && v > 0) out[k] = v
    }
    return out
  } catch {
    return {}
  }
}

/**
 * 表头单元格：右侧提供拖拽手柄。列宽由 ResizableTable 统一维护，
 * 拖拽只更新对应列的宽度，不影响排序/筛选等原生表头行为。
 */
function ResizableHeaderCell(props: any) {
  const { onResize, onReset, width, children, ...rest } = props
  const drag = useRef<{ x: number; width: number } | null>(null)

  // 组件在拖拽过程中卸载时，避免全局光标与选中状态残留。
  useEffect(() => () => {
    document.body.classList.remove('col-resizing')
  }, [])

  const onPointerDown = (e: PointerEvent<HTMLSpanElement>) => {
    if (!onResize) return
    e.preventDefault()
    e.stopPropagation()
    const th = e.currentTarget.parentElement as HTMLElement | null
    const startWidth = th ? th.getBoundingClientRect().width : Number(width) || 120
    drag.current = { x: e.clientX, width: startWidth }
    e.currentTarget.setPointerCapture(e.pointerId)
    document.body.classList.add('col-resizing')
  }

  const onPointerMove = (e: PointerEvent<HTMLSpanElement>) => {
    if (!drag.current) return
    onResize(drag.current.width + e.clientX - drag.current.x)
  }

  const endDrag = (e: PointerEvent<HTMLSpanElement>) => {
    if (!drag.current) return
    drag.current = null
    document.body.classList.remove('col-resizing')
    try {
      e.currentTarget.releasePointerCapture(e.pointerId)
    } catch {
      // 指针已被浏览器释放。
    }
  }

  // 键盘可调整，左右方向键每次 16px。
  const onKeyDown = (e: KeyboardEvent<HTMLSpanElement>) => {
    if (!onResize || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return
    e.preventDefault()
    e.stopPropagation()
    const th = e.currentTarget.parentElement as HTMLElement | null
    const current = th ? th.getBoundingClientRect().width : Number(width) || 120
    onResize(current + (e.key === 'ArrowRight' ? 16 : -16))
  }

  const style = { ...(rest.style || {}) }
  // 固定列的 sticky 定位由 antd 的 class 提供，不能被 inline relative 覆盖。
  const fixed = typeof rest.className === 'string' && rest.className.includes('ant-table-cell-fix')
  if (!style.position && !fixed) style.position = 'relative'

  return (
    <th {...rest} style={style}>
      {children}
      {onResize && (
        <span
          className="col-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label="调整列宽"
          title="拖拽调整列宽；双击恢复默认"
          tabIndex={0}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={endDrag}
          onPointerCancel={endDrag}
          onClick={e => e.stopPropagation()}
          onDoubleClick={e => { e.stopPropagation(); onReset?.() }}
          onKeyDown={onKeyDown}
        />
      )}
    </th>
  )
}

export default function ResizableTable<T extends object = any>({
  columns = [], storageKey, minWidth = 64, ...rest
}: ResizableTableProps<T>) {
  const [widths, setWidths] = useState<Widths>(() => loadWidths(storageKey))

  // 拖拽过程中会高频更新宽度；延迟写入，避免每移动 1px 都同步写 localStorage。
  useEffect(() => {
    if (!storageKey) return
    const timer = setTimeout(() => {
      try {
        localStorage.setItem(storageKey, JSON.stringify(widths))
      } catch {
        // 隐私模式或配额不足时忽略持久化。
      }
    }, 200)
    return () => clearTimeout(timer)
  }, [storageKey, widths])

  const resizedColumns = useMemo(
    () => (columns || []).map((col: any, index: number) => {
      // 列定义可设置 resizable: false 关闭拖拽（例如行末的操作列）。
      const { resizable: canResize = true, ...column } = col
      if (!canResize) return column
      const key = columnKey(column, index)
      const stored = widths[key]
      const width = stored != null ? Math.max(minWidth, stored) : column.width
      const original = typeof column.onHeaderCell === 'function' ? column.onHeaderCell(column) : {}
      return {
        ...column,
        ...(width !== undefined ? { width } : {}),
        onHeaderCell: () => ({
          ...original,
          width,
          onResize: (next: number) => setWidths(prev => {
            const clamped = Math.max(minWidth, Math.round(next))
            return prev[key] === clamped ? prev : { ...prev, [key]: clamped }
          }),
          onReset: () => setWidths(prev => {
            if (!(key in prev)) return prev
            const next = { ...prev }
            delete next[key]
            return next
          }),
        }),
      }
    }),
    [columns, widths, minWidth],
  )

  // 目前用于扁平列的结果明细表；rowSelection / expandable 附加列不计入总宽。
  const totalWidth = resizedColumns.reduce(
    (sum, col: any) => sum + (typeof col.width === 'number' ? col.width : 120),
    0,
  )
  const components = rest.components as any

  return (
    <ScrollTable<T>
      {...rest}
      columns={resizedColumns as any}
      scroll={{ ...(rest.scroll || {}), x: totalWidth }}
      components={{
        ...(components || {}),
        header: { ...(components?.header || {}), cell: ResizableHeaderCell },
      }}
    />
  )
}
