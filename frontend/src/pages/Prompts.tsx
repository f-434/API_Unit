import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Button, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag, Tooltip, Upload, message,
} from 'antd'
import { DeleteOutlined, DownloadOutlined, EditOutlined, ImportOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Prompt } from '../types'
import { PageHeader, EllipsisText } from '../components/common'
import { fmtTime } from '../format'

export default function Prompts() {
  const qc = useQueryClient()
  const { data = [], isLoading } = useQuery({ queryKey: ['prompts'], queryFn: api.prompts, select: d => d.items })
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Prompt | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [importText, setImportText] = useState('')
  const [q, setQ] = useState('')
  const [form] = Form.useForm()
  const content = Form.useWatch('content', form) as string | undefined
  const [tokenCount, setTokenCount] = useState<number>()
  const [counting, setCounting] = useState(false)

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
    form.setFieldsValue({ category: 'custom' })
    setOpen(true)
  }
  const openEdit = (p: Prompt) => {
    setEditing(p)
    form.setFieldsValue({ name: p.name, category: p.category, content: p.content })
    setOpen(true)
  }

  const save = useMutation({
    mutationFn: (v: any) => editing ? api.updatePrompt(editing.id, v) : api.createPrompt(v),
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

  const columns = [
    { title: '名称', dataIndex: 'name', width: 200, render: (v: string, r: Prompt) => <Space><b>{v}</b>{r.builtin && <Tag color="blue">内置</Tag>}</Space> },
    { title: '分类', dataIndex: 'category', width: 120, render: (v: string) => <Tag>{v}</Tag> },
    { title: '内容', dataIndex: 'content', render: (v: string) => <EllipsisText text={v} width={520} /> },
    { title: '长度', width: 90, render: (_: any, r: Prompt) => <span className="muted">{r.content.length} 字</span> },
    { title: '更新', dataIndex: 'updatedAt', width: 170, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
    {
      title: '操作', width: 110,
      render: (_: any, r: Prompt) => (
        <Space size={2}>
          <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          {!r.builtin && (
            <Popconfirm title="删除该提示词？" onConfirm={() => del.mutate(r.id)}>
              <Button size="small" type="text" danger icon={<DeleteOutlined />} />
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
        subtitle="用于 Token 审计、请求差异审计；压测也可以选择提示词轮换。内置提示词可直接编辑内容，但不能删除。"
        extra={[
          <Input key="q" allowClear prefix={<SearchOutlined />} placeholder="搜索名称/内容" value={q} onChange={e => setQ(e.target.value)} style={{ width: 220 }} />,
          <Button key="import" icon={<ImportOutlined />} onClick={() => setImportOpen(true)}>导入</Button>,
          <Button key="export" icon={<DownloadOutlined />} onClick={exportData}>导出</Button>,
          <Button key="add" type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建提示词</Button>,
        ]}
      />
      <div className="panel">
        <Table rowKey="id" loading={isLoading} dataSource={filtered} columns={columns as any} pagination={{ pageSize: 20, showSizeChanger: true }} />
      </div>

      <Modal open={open} title={editing ? '编辑提示词' : '新建提示词'} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={save.isPending} width={720} destroyOnClose>
        <Form form={form} layout="vertical" onFinish={v => save.mutate(v)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}><Input placeholder="例如：长中文审计" /></Form.Item>
          <Form.Item name="category" label="分类"><Input placeholder="custom / builtin" /></Form.Item>
          <Form.Item
            name="content"
            label="提示词内容"
            rules={[{ required: true, message: '请输入内容' }]}
            extra={<span className="muted">预估输入 ≈ {counting ? '计算中…' : tokenCount === undefined ? '-' : tokenCount.toLocaleString('zh-CN')} tok（tiktoken / gpt-4o，含消息开销）</span>}
          >
            <Input.TextArea rows={12} className="code-input" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal open={importOpen} title="导入提示词" onCancel={() => setImportOpen(false)} onOk={() => doImport.mutate()} confirmLoading={doImport.isPending} width={720}>
        <p className="muted">粘贴 JSON 数组，元素包含 name、category、content。</p>
        <Input.TextArea rows={14} className="code-input" value={importText} onChange={e => setImportText(e.target.value)} placeholder='[{"name":"示例","content":"..."}]' />
      </Modal>
    </>
  )
}
