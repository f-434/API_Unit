import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, AutoComplete, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, message,
} from 'antd'
import { DeleteOutlined, DownloadOutlined, EditOutlined, ImportOutlined, PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Attack } from '../types'
import { EllipsisText, PageHeader, SeverityTag } from '../components/common'
import { categoryLabel, fmtTime } from '../format'

export default function Attacks() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['attacks'], queryFn: api.attacks })
  const categories = data?.categories || []
  const list = data?.items || []
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Attack | null>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [importText, setImportText] = useState('')
  const [q, setQ] = useState('')
  const [cat, setCat] = useState<string>()
  const [sev, setSev] = useState<string>()
  const [form] = Form.useForm()

  const filtered = useMemo(() => {
    return list.filter(a =>
      (!cat || a.category === cat) &&
      (!sev || a.severity === sev) &&
      (!q || a.name.toLowerCase().includes(q.toLowerCase()) || a.prompt.toLowerCase().includes(q.toLowerCase())),
    )
  }, [list, q, cat, sev])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ severity: 'high', requireJudge: false, checks: '[]', category: categories[0]?.id || 'instruction_override' })
    setOpen(true)
  }
  const openEdit = (a: Attack) => {
    setEditing(a)
    form.setFieldsValue({
      ...a,
      checks: JSON.stringify(a.checks || [], null, 2),
    })
    setOpen(true)
  }

  const save = useMutation({
    mutationFn: async (v: any) => {
      let checks: any[] = []
      try { checks = JSON.parse(v.checks || '[]') } catch { throw new Error('checks 不是有效 JSON') }
      if (!Array.isArray(checks)) throw new Error('checks 必须是数组')
      const body = { ...v, checks }
      return editing ? api.updateAttack(editing.id, body) : api.createAttack(body)
    },
    onSuccess: () => {
      message.success(editing ? '已更新用例' : '已创建用例')
      setOpen(false)
      qc.invalidateQueries({ queryKey: ['attacks'] })
    },
    onError: (e: any) => message.error(e?.message || '保存失败'),
  })
  const del = useMutation({
    mutationFn: (id: string) => api.deleteAttack(id),
    onSuccess: () => { message.success('已删除'); qc.invalidateQueries({ queryKey: ['attacks'] }) },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })
  const reset = useMutation({
    mutationFn: api.resetAttacks,
    onSuccess: (r: any) => { message.success(`已恢复 ${r.count} 个内置用例`); qc.invalidateQueries({ queryKey: ['attacks'] }) },
    onError: (e: any) => message.error(e?.message || '恢复失败'),
  })
  const doImport = useMutation({
    mutationFn: async () => {
      const parsed = JSON.parse(importText)
      const attacks = Array.isArray(parsed) ? parsed : parsed.attacks
      if (!Array.isArray(attacks)) throw new Error('JSON 需要是数组，或包含 attacks 数组')
      return api.importAttacks(attacks)
    },
    onSuccess: (r: any) => {
      message.success(`已导入 ${r.created} 个用例`)
      setImportOpen(false)
      setImportText('')
      qc.invalidateQueries({ queryKey: ['attacks'] })
    },
    onError: (e: any) => message.error(e?.message || '导入失败'),
  })

  const exportData = () => {
    const blob = new Blob([JSON.stringify({ attacks: filtered }, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'apiunit-attacks.json'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const columns = [
    { title: '用例', dataIndex: 'name', width: 220, render: (v: string, r: Attack) => <Space><b>{v}</b>{r.builtin && <Tag color="blue">内置</Tag>}</Space> },
    { title: '分类', dataIndex: 'category', width: 140, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
    { title: '严重度', dataIndex: 'severity', width: 90, render: (v: string) => <SeverityTag severity={v} /> },
    { title: '裁判', dataIndex: 'requireJudge', width: 90, render: (v: boolean) => v ? <Tag color="purple">需要 LLM</Tag> : <Tag>规则</Tag> },
    { title: 'Canary', dataIndex: 'canary', width: 200, render: (v: string) => v ? <span className="small-mono">{v}</span> : <span className="muted">-</span> },
    { title: '攻击提示词', dataIndex: 'prompt', render: (v: string) => <EllipsisText text={v} width={420} /> },
    { title: '更新', dataIndex: 'updatedAt', width: 165, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
    {
      title: '操作', width: 100,
      render: (_: any, r: Attack) => (
        <Space size={2}>
          <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          {!r.builtin && (
            <Popconfirm title="删除该用例？" onConfirm={() => del.mutate(r.id)}>
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
        title="攻击用例"
        subtitle="内置规则类用例使用 canary 泄露、关键词/正则与拒绝话术判定；越狱类用例可在混合模式下交给 LLM 裁判。"
        extra={[
          <Input key="q" allowClear prefix={<SearchOutlined />} placeholder="搜索用例" value={q} onChange={e => setQ(e.target.value)} style={{ width: 200 }} />,
          <Select key="c" allowClear placeholder="分类" value={cat} onChange={setCat} options={categories.map(c => ({ value: c.id, label: c.label }))} style={{ width: 150 }} />,
          <Select key="s" allowClear placeholder="严重度" value={sev} onChange={setSev} options={['critical', 'high', 'medium', 'low'].map(s => ({ value: s, label: s }))} style={{ width: 110 }} />,
          <Button key="i" icon={<ImportOutlined />} onClick={() => setImportOpen(true)}>导入</Button>,
          <Button key="e" icon={<DownloadOutlined />} onClick={exportData}>导出</Button>,
          <Popconfirm key="r" title="恢复内置用例？" description="会删除并重新写入所有内置用例，自定义用例不受影响。" onConfirm={() => reset.mutate()}>
            <Button icon={<ReloadOutlined />}>恢复内置</Button>
          </Popconfirm>,
          <Button key="a" type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建用例</Button>,
        ]}
      />
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 14 }}
        message="安全测试说明"
        description="所有内置用例只使用无害标记（如 canary、OVERRIDE-OK），用于检测模型是否执行越权指令或泄露系统提示；不包含可用于真实攻击的内容。"
      />
      <div className="panel">
        <Table rowKey="id" loading={isLoading} dataSource={filtered} columns={columns as any} pagination={{ pageSize: 20, showSizeChanger: true }} />
      </div>

      <Modal open={open} title={editing ? '编辑攻击用例' : '新建攻击用例'} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={save.isPending} width={820} destroyOnClose>
        <Form form={form} layout="vertical" onFinish={v => save.mutate(v)}>
          <Space style={{ width: '100%' }} align="start">
            <Form.Item name="name" label="用例名称" rules={[{ required: true }]} style={{ width: 300 }}><Input /></Form.Item>
            <Form.Item name="category" label="分类" style={{ width: 220 }}>
              <AutoComplete options={categories.map(c => ({ value: c.id, label: `${c.label} (${c.id})` }))} placeholder="选择或输入分类 ID" />
            </Form.Item>
            <Form.Item name="severity" label="严重度" style={{ width: 140 }}>
              <Select options={['critical', 'high', 'medium', 'low'].map(s => ({ value: s, label: s }))} />
            </Form.Item>
          </Space>
          <Form.Item name="systemPrompt" label="System Prompt"><Input.TextArea rows={4} className="code-input" /></Form.Item>
          <Form.Item name="prompt" label="攻击提示词" rules={[{ required: true }]}><Input.TextArea rows={5} className="code-input" /></Form.Item>
          <Space style={{ width: '100%' }} align="start">
            <Form.Item name="canary" label="Canary 标记" style={{ width: 320 }} extra="系统提示中不要泄露该值；命中即判定攻击成功。"><Input /></Form.Item>
            <Form.Item name="requireJudge" label="规则无法判定时交给 LLM 裁判" valuePropName="checked" style={{ paddingTop: 30 }}>
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item name="checks" label="确定性检查（JSON 数组）" extra='示例：[{"kind":"contains","value":"OVERRIDE-OK"}]，kind 支持 canary_leak / contains / regex / refusal'>
            <Input.TextArea rows={5} className="code-input" />
          </Form.Item>
          <Form.Item name="rubric" label="裁判判定要点（可选）"><Input.TextArea rows={3} /></Form.Item>
        </Form>
      </Modal>

      <Modal open={importOpen} title="导入攻击用例" onCancel={() => setImportOpen(false)} onOk={() => doImport.mutate()} confirmLoading={doImport.isPending} width={760}>
        <p className="muted">粘贴 JSON 数组或包含 attacks 数组的对象。</p>
        <Input.TextArea rows={14} className="code-input" value={importText} onChange={e => setImportText(e.target.value)} />
      </Modal>
    </>
  )
}
