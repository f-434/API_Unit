import ScrollTable from '../components/ScrollTable'
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Descriptions, Input, InputNumber, Popconfirm, Space, Tag, message } from 'antd'
import {
  CloudDownloadOutlined, DeleteOutlined, PlusOutlined, ReloadOutlined, SaveOutlined, SyncOutlined,
} from '@ant-design/icons'
import { api } from '../api'
import type { TokenFamilyNode } from '../types'
import { EllipsisText, PageHeader } from '../components/common'
import AuthenticityHostedSettings from '../components/AuthenticityHostedSettings'
import { fmtDuration } from '../format'

function updateFamily(
  nodes: TokenFamilyNode[], path: number[], patch: Partial<TokenFamilyNode>,
): TokenFamilyNode[] {
  if (!path.length) return nodes
  const [head, ...rest] = path
  return nodes.map((node, i) => {
    if (i !== head) return node
    if (!rest.length) return { ...node, ...patch }
    return { ...node, children: updateFamily(node.children || [], rest, patch) }
  })
}

function addFamilyChild(nodes: TokenFamilyNode[], path: number[]): TokenFamilyNode[] {
  if (!path.length) return nodes
  const [head, ...rest] = path
  return nodes.map((node, i) => {
    if (i !== head) return node
    if (!rest.length) {
      return { ...node, children: [...(node.children || []), { key: '', label: '' }] }
    }
    return { ...node, children: addFamilyChild(node.children || [], rest) }
  })
}

function removeFamily(nodes: TokenFamilyNode[], path: number[]): TokenFamilyNode[] {
  if (!path.length) return nodes
  const [head, ...rest] = path
  if (!rest.length) return nodes.filter((_, i) => i !== head)
  return nodes.map((node, i) => i === head
    ? { ...node, children: removeFamily(node.children || [], rest) }
    : node)
}

// 口径固定为 家族 → 平台 → 模型 三级。
const MAX_FAMILY_DEPTH = 3

// 与后端 tokenmeta.ModelKey 保持一致：小写、去掉 models/ 前缀、非法字符折叠为短横线。
function sanitizeKey(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/^models\//, '')
    .replace(/[^a-z0-9._:@-]+/g, '-')
    .replace(/^[^a-z0-9]+/, '')
    .replace(/-+$/, '')
}

// 计算某个节点下已有多少个子节点，用于定位刚新增的空节点。
function childCountAt(nodes: TokenFamilyNode[], path: number[]): number {
  let cur = nodes
  for (let i = 0; i < path.length; i++) {
    const node = cur[path[i]]
    if (!node) return 0
    if (i === path.length - 1) return node.children?.length || 0
    cur = node.children || []
  }
  return nodes.length
}

function keyPlaceholder(depth: number): string {
  if (depth === 0) return '家族标识'
  if (depth === 1) return '平台标识'
  return '模型 ID（/ 用 - 代替）'
}

function familyTreeValid(nodes: TokenFamilyNode[], depth = 1): boolean {
  if (!nodes.length || depth > MAX_FAMILY_DEPTH) return false
  for (const node of nodes) {
    if (!node.key.trim() || !node.label.trim()) return false
    if (node.children?.length && !familyTreeValid(node.children, depth + 1)) return false
  }
  return true
}

function FamilyRows({ nodes, path, editingKey, onPatch, onAdd, onRemove, onBlur }: {
  nodes: TokenFamilyNode[]
  path: number[]
  editingKey: string | null
  onPatch: (path: number[], patch: Partial<TokenFamilyNode>) => void
  onAdd: (path: number[]) => void
  onRemove: (path: number[]) => void
  onBlur: () => void
}) {
  return (
    <>
      {nodes.map((node, i) => {
        const p = [...path, i]
        const keyPath = p.join('.')
        // 新节点的标识在失焦前保持可编辑，避免输入第一个字符后被替换成只读文本。
        const keyEditable = !node.key || editingKey === keyPath
        return (
          <div key={keyPath}>
            <div className="caliber-row" data-depth={path.length}>
              <Input
                size="small"
                className="caliber-name"
                value={node.label}
                placeholder="名称"
                onChange={e => onPatch(p, { label: e.target.value })}
              />
              {keyEditable
                ? <Input
                    size="small"
                    value={node.key}
                    placeholder={keyPlaceholder(path.length)}
                    onChange={e => onPatch(p, { key: sanitizeKey(e.target.value) })}
                    onBlur={onBlur}
                  />
                : <span className="caliber-key mono">{node.key}</span>}
              <div className="caliber-actions">
                {path.length < MAX_FAMILY_DEPTH - 1 && (
                  <Button size="small" type="text" icon={<PlusOutlined />} title="新增下级" onClick={() => onAdd(p)} />
                )}
                <Button size="small" type="text" danger icon={<DeleteOutlined />} title="删除" onClick={() => onRemove(p)} />
              </div>
            </div>
            {(node.children?.length || 0) > 0 && (
              <div className="caliber-children">
                <FamilyRows
                  nodes={node.children || []}
                  path={p}
                  editingKey={editingKey}
                  onPatch={onPatch}
                  onAdd={onAdd}
                  onRemove={onRemove}
                  onBlur={onBlur}
                />
              </div>
            )}
          </div>
        )
      })}
    </>
  )
}

export default function Settings() {
  const qc = useQueryClient()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.settings })
  const { data: meta, refetch: refetchMeta, isFetching: metaFetching } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const [days, setDays] = useState(30)

  const refresh = () => {
    refetchMeta()
    qc.invalidateQueries({ queryKey: ['settings'] })
  }

  const ensureGemini = useMutation({
    mutationFn: api.ensureGeminiTokenizer,
    onSuccess: (r: any) => {
      if (r.ok) message.success('Gemini 本地分词器已就绪')
      else message.error(r.error || '下载失败', 8)
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '下载失败', 8)
      refresh()
    },
  })

  const reloadTokenizers = useMutation({
    mutationFn: api.reloadTokenizers,
    onSuccess: (r: any) => {
      if (r.ok) message.success('本地分词器已重新加载')
      else message.error(r.error || '加载失败', 8)
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '加载失败', 8)
      refresh()
    },
  })

  const resetGemini = useMutation({
    mutationFn: (download: boolean) => api.resetGeminiTokenizer(download),
    onSuccess: (r: any, download: boolean) => {
      if (r.ok) {
        message.success(download ? '已清除并重新下载 Gemini 分词器' : '已清除本地 Gemini 分词器文件')
      } else {
        message.error(r.error || (download ? '重新下载失败' : '清除失败'), 8)
      }
      refresh()
    },
    onError: (e: any) => {
      message.error(e?.message || '操作失败', 8)
      refresh()
    },
  })
  const purge = useMutation({
    mutationFn: () => api.purgeRuns(days),
    onSuccess: (r: any) => { message.success(`已清理 ${r.deleted} 条历史测试`); qc.invalidateQueries({ queryKey: ['runs'] }) },
    onError: (e: any) => message.error(e?.message || '清理失败'),
  })

  // 编辑中的口径树不能被窗口聚焦触发的后台刷新覆盖。
  const { data: familyData } = useQuery({
    queryKey: ['tokenFamilies'],
    queryFn: api.tokenFamilies,
    refetchOnWindowFocus: false,
    staleTime: 5 * 60 * 1000,
  })
  const [families, setFamilies] = useState<TokenFamilyNode[] | null>(null)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  useEffect(() => { if (familyData?.items) setFamilies(familyData.items) }, [familyData])
  const tree = families ?? familyData?.items ?? []
  const editTree = (fn: (prev: TokenFamilyNode[]) => TokenFamilyNode[]) =>
    setFamilies(prev => fn(prev ?? familyData?.items ?? []))
  const saveFamilies = useMutation({
    mutationFn: (items: TokenFamilyNode[]) => api.saveTokenFamilies(items),
    onSuccess: (r: any) => {
      setFamilies(r.items)
      setEditingKey(null)
      message.success('已保存 Token 口径')
      qc.invalidateQueries({ queryKey: ['meta'] })
      qc.invalidateQueries({ queryKey: ['tokenFamilies'] })
    },
    onError: (e: any) => message.error(e?.message || '保存失败'),
  })

  const tokenizerRows = meta?.tokenizers?.gemini || []
  const tokenizerErrors = tokenizerRows.filter(r => !!r.error)
  const tokenizerDir = meta?.tokenizers?.tokenizerDir || ''
  const offline = meta?.tokenizers?.offline ?? settings?.offline ?? false

  return (
    <>
      <PageHeader title="设置" />
      <div className="panel">
        <div className="panel-title">运行信息</div>
        <Descriptions size="small" column={2} bordered>
          <Descriptions.Item label="版本">{settings?.version || '-'}</Descriptions.Item>
          <Descriptions.Item label="运行时长">{fmtDuration((settings?.uptimeSec || 0) * 1000)}</Descriptions.Item>
          <Descriptions.Item label="数据目录"><span className="small-mono">{settings?.dataDir || '-'}</span></Descriptions.Item>
          <Descriptions.Item label="前端目录"><span className="small-mono">{settings?.webDir || '-'}</span></Descriptions.Item>
          <Descriptions.Item label="访问密码">{settings?.authEnabled ? <Tag color="green">已启用</Tag> : <Tag>未启用</Tag>}</Descriptions.Item>
          <Descriptions.Item label="离线模式">{settings?.offline ? <Tag color="orange">是（禁止下载分词器）</Tag> : <Tag>否</Tag>}</Descriptions.Item>
          <Descriptions.Item label="保留天数">{settings?.retentionDays ?? '-'} 天</Descriptions.Item>
          <Descriptions.Item label="当前运行中的测试">{settings?.activeRuns ?? 0} / {settings?.maxConcurrentRuns ?? '-'}</Descriptions.Item>
        </Descriptions>
      </div>

      <div className="panel">
        <div className="panel-title">本地分词器</div>
        {offline && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="离线模式：无法自动下载 Gemini 分词器"
            description={<span>请手动将模型文件放入：<span className="small-mono" style={{ wordBreak: 'break-all' }}>{tokenizerDir || '-'}</span></span>}
          />
        )}
        {!!tokenizerErrors.length && (
          <Alert
            type="error"
            showIcon
            style={{ marginBottom: 12 }}
            message="Gemini 本地分词器异常"
            description={<div className="tokenizer-error">{tokenizerErrors.map(r => `${r.name}：${r.error}`).join('\n')}</div>}
            action={(
              <Popconfirm
                title="清除并重新下载 Gemini 分词器？"
                description="将删除本地模型文件并重新下载校验，网络较慢时可能需要几分钟。"
                onConfirm={() => resetGemini.mutate(true)}
              >
                <Button danger size="small" loading={resetGemini.isPending} disabled={offline}>清除并重新下载</Button>
              </Popconfirm>
            )}
          />
        )}
        <Space wrap style={{ marginBottom: 10 }}>
          <Tag color={meta?.tokenizers?.tiktoken ? 'green' : 'default'}>tiktoken：{meta?.tokenizers?.tiktoken ? '已内置' : '不可用'}</Tag>
          <Tag color={offline ? 'orange' : 'blue'}>{offline ? '离线模式' : '在线模式'}</Tag>
          <Button icon={<CloudDownloadOutlined />} loading={ensureGemini.isPending} onClick={() => ensureGemini.mutate()} disabled={offline}>下载 / 校验 Gemini 分词器</Button>
          <Button icon={<SyncOutlined />} loading={reloadTokenizers.isPending} onClick={() => reloadTokenizers.mutate()}>重新加载本地分词器</Button>
          <Popconfirm
            title="清除并重新下载 Gemini 分词器？"
            description="将删除本地模型文件并重新下载校验，网络较慢时可能需要几分钟。"
            onConfirm={() => resetGemini.mutate(true)}
          >
            <Button danger icon={<DeleteOutlined />} loading={resetGemini.isPending} disabled={offline}>清除并重新下载</Button>
          </Popconfirm>
          <Popconfirm title="仅清除本地分词器文件？" onConfirm={() => resetGemini.mutate(false)}>
            <Button size="small" type="text" loading={resetGemini.isPending}>仅清除文件</Button>
          </Popconfirm>
          <Button icon={<ReloadOutlined />} loading={metaFetching} onClick={refresh}>刷新</Button>
        </Space>
        {tokenizerDir && (
          <div className="field-label" style={{ marginBottom: 10, wordBreak: 'break-all' }}>
            分词器目录 <span className="small-mono">{tokenizerDir}</span>
          </div>
        )}
        <ScrollTable
          rowKey="name"
          size="small"
          pagination={false}
          dataSource={tokenizerRows}
          tableLayout="fixed"
          scroll={{ x: 860 }}
          columns={[
            { title: 'Tokenizer', dataIndex: 'name', width: 110 },
            { title: '文件', dataIndex: 'file', width: 240, render: (v: string) => <span className="small-mono">{v}</span> },
            { title: '已下载', dataIndex: 'downloaded', width: 80, align: 'center' as const, render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            { title: '已加载', dataIndex: 'loaded', width: 80, align: 'center' as const, render: (v: boolean) => v ? <Tag color="green">是</Tag> : <Tag>否</Tag> },
            {
              title: '错误', dataIndex: 'error', width: 350,
              render: (v: string) => v
                ? <span style={{ color: '#dc2626' }}><EllipsisText text={v} width={330} /></span>
                : <span className="muted">-</span>,
            },
          ]}
        />
      </div>

      <div className="panel">
        <div className="panel-title">
          <span>Token 口径</span>
          <span style={{ flex: 1 }} />
          <Button size="small" icon={<PlusOutlined />} onClick={() => {
            setEditingKey(String(tree.length))
            editTree(prev => [...prev, { key: '', label: '' }])
          }}>
            新增一级
          </Button>
          <Button
            size="small"
            type="primary"
            icon={<SaveOutlined />}
            loading={saveFamilies.isPending}
            onClick={() => {
              if (!tree.length) return message.error('至少保留一个根节点')
              if (!familyTreeValid(tree)) return message.error('请填写所有节点的名称和标识')
              saveFamilies.mutate(tree)
            }}
          >
            保存
          </Button>
        </div>
        <div className="caliber-tree">
          <div className="caliber-head">
            <span>名称</span>
            <span>标识</span>
            <span />
          </div>
          {tree.length === 0
            ? <div className="caliber-empty muted">尚无口径，先新增一级</div>
            : (
              <FamilyRows
                nodes={tree}
                path={[]}
                editingKey={editingKey}
                onPatch={(path, patch) => editTree(prev => updateFamily(prev, path, patch))}
                onAdd={path => {
                  setEditingKey([...path, childCountAt(tree, path)].join('.'))
                  editTree(prev => addFamilyChild(prev, path))
                }}
                onRemove={path => {
                  setEditingKey(null)
                  editTree(prev => removeFamily(prev, path))
                }}
                onBlur={() => setEditingKey(null)}
              />
            )}
        </div>
      </div>

      <AuthenticityHostedSettings />

      <div className="panel">
        <div className="panel-title">数据清理</div>
        <Space>
          <span>保留最近</span>
          <InputNumber min={1} max={3650} value={days} onChange={v => setDays(Number(v || 30))} />
          <span>天，立即清理更早的数据</span>
          <Button danger icon={<DeleteOutlined />} loading={purge.isPending} onClick={() => purge.mutate()}>执行清理</Button>
        </Space>
      </div>

    </>
  )
}
