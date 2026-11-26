import ScrollTable from '../components/ScrollTable'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '../pagination'
import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Button, Checkbox, Form, Input, InputNumber, Modal, Popconfirm, Space, Tag, Tooltip, message,
} from 'antd'
import { DeleteOutlined, DownloadOutlined, EditOutlined, ImportOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Prompt, TokenFamilyNode } from '../types'
import { PageHeader, EllipsisText } from '../components/common'
import { fmtTime } from '../format'

type FamilyRow = { path: string; label: string; depth: number }

// 把口径树拍平为“缩进 + 路径”，一级节点本身也可维护默认值。
function flattenFamilies(nodes: TokenFamilyNode[], prefix = '', depth = 0): FamilyRow[] {
  const out: FamilyRow[] = []
  for (const n of nodes) {
    const path = prefix ? `${prefix}/${n.key}` : n.key
    out.push({ path, label: n.label, depth })
    out.push(...flattenFamilies(n.children || [], path, depth + 1))
  }
  return out
}

function cleanOverrides(raw: any): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [key, value] of Object.entries(raw || {})) {
    const n = Number(value)
    if (Number.isFinite(n) && n > 0) out[key] = Math.round(n)
  }
  return out
}

export default function Prompts() {
  const qc = useQueryClient()
  const { data = [], isLoading } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts, select: d => d.items })
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta })
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Prompt | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [importText, setImportText] = useState('')
  const [q, setQ] = useState('')
  const [form] = Form.useForm()
  const content = Form.useWatch('content', form) as string | undefined
  const hasOverrides = Form.useWatch('tokenOverrides', form) as Record<string, number> | undefined
  const [tokenCount, setTokenCount] = useState<number>()
  const [counting, setCounting] = useState(false)

  const familyRows = useMemo(() => flattenFamilies(meta?.tokenFamilies || []), [meta])
  const familyLabels = useMemo(() => {
    const out: Record<string, string> = {}
    for (const row of familyRows) out[row.path] = row.label
    return out
  }, [familyRows])

  useEffect(() => {
    if (!open) { setTokenCount(undefined); setCounting(false); return }
    const text = (content || '').trim()
    if (!text) { setTokenCount(0); setCounting(false); return }
    let cancelled = false
    setCounting(true)
    const timer = setTimeout(() => {
      api.tokenizerCount({
        model: 'gpt-4o', strategy: 'tiktoken',
        messages: [{ role: 'user', content: text }],
      }).then((r: any) => {
        if (!cancelled) setTokenCount(Number(r?.tokens || 0))
      }).catch(() => {
        if (!cancelled) setTokenCount(undefined)
      }).finally(() => {
        if (!cancelled) setCounting(false)
      })
    }, 300)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [open, content])

  const filtered = useMemo(() => {
    if (!q.trim()) return data
    const s = q.toLowerCase()
    return data.filter(p => p.name.toLowerCase().includes(s) || p.content.toLowerCase().includes(s) || p.category.toLowerCase().includes(s))
  }, [data, q])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ category: 'custom', tokenOverrides: {}, recalibrate: false })
    setOpen(true)
  }
  const openEdit = (p: Prompt) => {
    setEditing(p)
    // 必须先清空表单：不同提示词的校准路径集合不同，
    // 残留字段会把上一条提示词的 Token 数带到当前提示词。
    form.resetFields()
    form.setFieldsValue({
      name: p.name, category: p.category, content: p.content,
      tokenOverrides: { ...(p.tokenOverrides || {}) }, recalibrate: false,
    })
    setOpen(true)
  }

  const save = useMutation({
    mutationFn: (v: any) => {
      const body = {
        name: v.name, category: v.category, content: v.content,
        tokenOverrides: cleanOverrides(v.tokenOverrides),
        recalibrate: !!v.recalibrate,
      }
      return editing ? api.updatePrompt(editing.id, body) : api.createPrompt(body)
    },
    onSuccess: () => {
      message.success(editing ? '已更新' : '已创建')
      setOpen(false)
      qc.invalidateQueries({ queryKey: ['prompts'] })
    },
    onError: (e: any) => message.error(e?.message || '保存失败'),
  })
  const del = useMutation({
    mutationFn: (id: string) => api.deletePrompt(id),
    onSuccess: () => { message.success('已删除'); qc.invalidateQueries({ queryKey: ['prompts'] }) },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })
  const doImport = useMutation({
    mutationFn: async () => {
      const parsed = JSON.parse(importText)
      const prompts = Array.isArray(parsed) ? parsed : parsed.prompts
      if (!Array.isArray(prompts)) throw new Error('JSON 需要是数组，或包含 prompts 数组')
      return api.importPrompts(prompts)
    },
    onSuccess: (r: any) => {
      message.success(`已导入 ${r.created} 个提示词`)
      setImportOpen(false)
      setImportText('')
      qc.invalidateQueries({ queryKey: ['prompts'] })
    },
    onError: (e: any) => message.error(e?.message || '导入失败'),
  })

  const exportData = () => {
    const blob = new Blob([JSON.stringify({ prompts: data }, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'apiunit-prompts.json'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const contentChanged = !!editing && content !== undefined && content !== editing.content
  const existingOverrides = Object.keys(cleanOverrides(hasOverrides)).length > 0

  const overrideCell = (p: Prompt) => {
    const entries = Object.entries(p.tokenOverrides || {})
    if (!entries.length) return <span className="muted">按分词器</span>
    const [path, tokens] = entries[0]
    return (
      <Tooltip title={(
        <div style={{ lineHeight: 1.7 }}>
          {entries.map(([key, value]) => (
            <div key={key}>{familyLabels[key] || key}：{value} tok</div>
          ))}
        </div>
      )}>
        <Space size={4} wrap>
          <Tag color={p.tokenOverridesStale ? 'default' : 'green'}>{familyLabels[path] || path} · {tokens}</Tag>
          {entries.length > 1 && <span className="muted">+{entries.length - 1}</span>}
          {p.tokenOverridesStale && <Tag color="orange">已失效</Tag>}
        </Space>
      </Tooltip>
    )
  }

  const columns = [
    { title: '名称', dataIndex: 'name', width: 190, render: (v: string, r: Prompt) => <Space><b>{v}</b>{r.builtin && <Tag color="blue">内置</Tag>}</Space> },
    { title: '分类', dataIndex: 'category', width: 110, render: (v: string) => <Tag>{v}</Tag> },
    { title: '内容', dataIndex: 'content', render: (v: string) => <EllipsisText text={v} width={380} /> },
    { title: '本地 Token', key: 'overrides', width: 210, render: (_: any, r: Prompt) => overrideCell(r) },
    { title: '更新', dataIndex: 'updatedAt', width: 170, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
    {
      title: '操作', width: 100,
      render: (_: any, r: Prompt) => (
        <Space size={2}>
          <Button size="small" type="text" icon={<EditOutlined />} title="编辑提示词" onClick={() => openEdit(r)} />
          {!r.builtin && (
            <Popconfirm title="删除该提示词？" onConfirm={() => del.mutate(r.id)}>
              <Button size="small" type="text" danger icon={<DeleteOutlined />} title="删除提示词" />
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="提示词库"
        extra={[
          <Input key="q" allowClear prefix={<SearchOutlined />} placeholder="搜索名称/内容" value={q} onChange={e => setQ(e.target.value)} style={{ width: 220 }} />,
          <Button key="import" icon={<ImportOutlined />} onClick={() => setImportOpen(true)}>导入</Button>,
          <Button key="export" icon={<DownloadOutlined />} onClick={exportData}>导出</Button>,
          <Button key="add" type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建提示词</Button>,
        ]}
      />
      <div className="panel">
        <ScrollTable rowKey="id" loading={isLoading} dataSource={filtered} columns={columns as any} pagination={{ pageSize: DEFAULT_PAGE_SIZE, pageSizeOptions: PAGE_SIZE_OPTIONS, showSizeChanger: true }} />
      </div>

      <Modal
        open={open}
        title={editing ? '编辑提示词' : '新建提示词'}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
        width={760}
        destroyOnClose
        styles={{ body: { maxHeight: '62vh', overflowY: 'auto', paddingRight: 8 } }}
      >
        <Form form={form} layout="vertical" preserve={false} onFinish={v => save.mutate(v)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}><Input placeholder="例如：长中文审计" /></Form.Item>
          <Form.Item name="category" label="分类"><Input placeholder="custom / builtin" /></Form.Item>
          <Form.Item
            name="content"
            label="提示词内容"
            rules={[{ required: true, message: '请输入内容' }]}
            extra={<span className="muted">tiktoken / gpt-4o 参考 ≈ {counting ? '计算中…' : tokenCount === undefined ? '-' : tokenCount.toLocaleString('zh-CN')} tok</span>}
          >
            <Input.TextArea rows={8} className="code-input" />
          </Form.Item>

          <div className="panel-title" style={{ marginTop: 4 }}>本地 Token 校准</div>
          {!!editing?.tokenOverridesStale && !contentChanged && (
            <div style={{ marginBottom: 6 }}><Tag color="orange">内容曾变更，校准值已失效；修改任一数值可重新校准</Tag></div>
          )}
          {contentChanged && existingOverrides && (
            <>
              <div style={{ marginBottom: 4 }}><Tag color="orange">内容已变更，未重新校准将回退本地分词器</Tag></div>
              <Form.Item name="recalibrate" valuePropName="checked" style={{ marginBottom: 6 }}>
                <Checkbox>沿用现有校准值，并按新内容重新校准</Checkbox>
              </Form.Item>
            </>
          )}
          <div style={{ border: '1px solid #f0f0f0', borderRadius: 6, padding: '6px 10px' }}>
            {familyRows.length === 0 && <span className="muted">加载口径中…</span>}
            {familyRows.map(row => (
              <div key={row.path} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '3px 0', paddingLeft: row.depth * 16 }}>
                <span style={{ flex: 1 }}>
                  {row.label}
                  {row.depth > 0 && <span className="muted small-mono" style={{ marginLeft: 8 }}>{row.path}</span>}
                </span>
                <Form.Item name={['tokenOverrides', row.path]} noStyle>
                  <InputNumber min={1} precision={0} placeholder="按分词器" style={{ width: 150 }} />
                </Form.Item>
              </div>
            ))}
          </div>
        </Form>
      </Modal>

      <Modal open={importOpen} title="导入提示词" onCancel={() => setImportOpen(false)} onOk={() => doImport.mutate()} confirmLoading={doImport.isPending} width={720}>
        <Input.TextArea rows={14} className="code-input" value={importText} onChange={e => setImportText(e.target.value)} placeholder='[{"name":"示例","category":"custom","content":"...", "tokenOverrides": {"gemini/vt": 32}}]' />
      </Modal>
    </>
  )
}
