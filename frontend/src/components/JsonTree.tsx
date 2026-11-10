import { useMemo } from 'react'
import { Tree } from 'antd'
import type { DataNode } from 'antd/es/tree'

export type HighlightMap = Record<string, 'add' | 'remove' | 'replace'>

const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/

export function joinPath(base: string, key: string) {
  return IDENT.test(key) ? `${base}.${key}` : `${base}['${key.replace(/'/g, "\\'")}']`
}

function prefixes(path: string): string[] {
  const out: string[] = ['$']
  const tokens = path.match(/\[[^\]]*\]|\.[^.[\]]+/g) || []
  let cur = '$'
  for (const t of tokens) {
    cur += t
    out.push(cur)
  }
  return out.slice(0, -1)
}

function typeColor(v: any) {
  if (v === null) return '#94a3b8'
  if (typeof v === 'string') return '#0f766e'
  if (typeof v === 'number') return '#b45309'
  if (typeof v === 'boolean') return '#7c3aed'
  return '#334155'
}

function displayScalar(v: any): string {
  if (v === null) return 'null'
  if (typeof v === 'string') {
    const s = v.length > 160 ? `${v.slice(0, 160)}…` : v
    return JSON.stringify(s)
  }
  if (typeof v === 'object') return ''
  return String(v)
}

const MAX_NODES = 4000

export default function JsonTree({ data, highlight, maxHeight = 560 }: {
  data: any
  highlight?: HighlightMap
  maxHeight?: number
}) {
  const { treeData, expandAll, nodeCount } = useMemo(() => {
    let count = 0
    const ancestors = new Set<string>()
    Object.keys(highlight || {}).forEach(p => prefixes(p).forEach(x => ancestors.add(x)))

    const build = (value: any, key: string, path: string, depth: number): DataNode | null => {
      if (count >= MAX_NODES) return null
      count++
      const isContainer = value !== null && typeof value === 'object'
      const entries: [string, any][] = isContainer
        ? Array.isArray(value)
          ? value.map((v, i) => [String(i), v])
          : Object.entries(value)
        : []
      let suffix = ''
      if (Array.isArray(value)) suffix = ` [${value.length}]`
      else if (isContainer) suffix = ` {${entries.length}}`
      const cls = highlight?.[path] ? `hl-${highlight[path]}` : ancestors.has(path) ? 'hl-ancestor' : ''
      const title = (
        <span className={`json-row ${cls}`}>
          <span className="json-key">{key}</span>
          {!isContainer && <span className="json-val" style={{ color: typeColor(value) }}>{displayScalar(value)}</span>}
          {isContainer && <span className="json-meta">{suffix}</span>}
        </span>
      )
      const children: DataNode[] = []
      if (isContainer && depth < 14) {
        if (Array.isArray(value)) {
          for (let i = 0; i < value.length; i++) {
            const node = build(value[i], `[${i}]`, `${path}[${i}]`, depth + 1)
            if (node) children.push(node)
          }
        } else {
          for (const [k, v] of entries) {
            const node = build(v, k, joinPath(path, k), depth + 1)
            if (node) children.push(node)
          }
        }
      }
      return { key: path, title, children }
    }

    const root = build(data, '$', '$', 0)
    return { treeData: root ? [root] : [], expandAll: count < 700, nodeCount: count }
  }, [data, highlight])

  return (
    <div className="json-tree" style={{ maxHeight, overflow: 'auto' }}>
      <Tree
        treeData={treeData}
        showLine
        blockNode
        selectable={false}
        defaultExpandAll={expandAll}
        defaultExpandedKeys={expandAll ? undefined : ['$', ...treeData[0]?.children?.slice(0, 12).map(c => String(c.key)) || []]}
      />
      {nodeCount >= MAX_NODES && <div className="muted" style={{ padding: 8 }}>JSON 过大，仅显示前 {MAX_NODES} 个节点。</div>}
    </div>
  )
}
