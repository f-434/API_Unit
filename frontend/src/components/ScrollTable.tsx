import { useEffect, useRef } from 'react'
import { Table } from 'antd'
import type { TableProps } from 'antd'

/**
 * Ant Design 只能在表格底部提供横向滚动条。这里在表格顶部补一条
 * 代理滚动条，并与底部原生滚动条双向同步，方便长表格左右滚动。
 * 顶部滚动条只在内容确实溢出时显示，避免占用无意义的垂直空间。
 */
export default function ScrollTable<T extends object = any>(props: TableProps<T>) {
  const tableRef = useRef<HTMLDivElement>(null)
  const topRef = useRef<HTMLDivElement>(null)
  const bodyRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    const top = topRef.current
    const table = tableRef.current
    if (!top || !table) return

    let raf = 0
    const inner = top.firstElementChild as HTMLElement | null

    const findBody = () => {
      const el = table.querySelector<HTMLElement>('.ant-table-body, .ant-table-content')
      if (el !== bodyRef.current) {
        bodyRef.current?.removeEventListener('scroll', onBodyScroll)
        bodyRef.current = el
        el?.addEventListener('scroll', onBodyScroll, { passive: true })
      }
      return el
    }

    const refresh = () => {
      const body = findBody()
      if (!body || !inner) return
      const overflow = body.scrollWidth > body.clientWidth + 1
      top.classList.toggle('is-visible', overflow)
      if (overflow) inner.style.width = `${body.scrollWidth}px`
      // 数据更新或容器尺寸变化后，保证顶部滚动条与表格位置一致。
      if (top.scrollLeft !== body.scrollLeft) top.scrollLeft = body.scrollLeft
    }

    const onTopScroll = () => {
      const body = bodyRef.current
      if (body && body.scrollLeft !== top.scrollLeft) body.scrollLeft = top.scrollLeft
    }

    const onBodyScroll = () => {
      if (bodyRef.current && top.scrollLeft !== bodyRef.current.scrollLeft) {
        top.scrollLeft = bodyRef.current.scrollLeft
      }
    }

    const schedule = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(refresh)
    }

    top.addEventListener('scroll', onTopScroll, { passive: true })
    const ro = new ResizeObserver(schedule)
    ro.observe(table)
    const mo = new MutationObserver(schedule)
    mo.observe(table, { childList: true, subtree: true })

    schedule()
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      mo.disconnect()
      top.removeEventListener('scroll', onTopScroll)
      bodyRef.current?.removeEventListener('scroll', onBodyScroll)
      bodyRef.current = null
    }
  }, [])

  return (
    <div className="scroll-table">
      <div className="scroll-table-top" ref={topRef} aria-hidden="true">
        <div className="scroll-table-top-inner" />
      </div>
      <div ref={tableRef}>
        <Table<T> {...props} />
      </div>
    </div>
  )
}
