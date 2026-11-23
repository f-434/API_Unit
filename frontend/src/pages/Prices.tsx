import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Form, Input, InputNumber, Modal, Popconfirm, Space, Table, Tag, message } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Price } from '../types'
import { PageHeader } from '../components/common'
import { fmtCost } from '../format'

export default function Prices() {
  const qc = useQueryClient()
  const { data = [], isLoading } = useQuery({ queryKey: ['prices'], queryFn: api.prices, select: d => d.items })
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Price | null>(null)
  const [form] = Form.useForm()

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ currency: 'USD', inputPerM: 0, outputPerM: 0, cacheReadPerM: 0, cacheWritePerM: 0 })
    setOpen(true)
  }
  const openEdit = (p: Price) => {
    setEditing(p)
    form.setFieldsValue(p)
    setOpen(true)
  }

  const save = useMutation({
    mutationFn: (v: any) => editing ? api.updatePrice(editing.id, v) : api.createPrice(v),
    onSuccess: () => { message.success('已保存'); setOpen(false); qc.invalidateQueries({ queryKey: ['prices'] }) },
    onError: (e: any) => message.error(e?.message || '保存失败'),
  })
  const del = useMutation({
    mutationFn: (id: string) => api.deletePrice(id),
    onSuccess: () => { message.success('已删除'); qc.invalidateQueries({ queryKey: ['prices'] }) },
  })

  const cols = [
    { title: '模型匹配', dataIndex: 'pattern', render: (v: string) => <span className="mono">{v}</span> },
    { title: '输入 / 1M', dataIndex: 'inputPerM', width: 130, render: (v: number, r: Price) => fmtCost(v, r.currency === 'CNY' ? '¥' : '$') },
    { title: '输出 / 1M', dataIndex: 'outputPerM', width: 130, render: (v: number, r: Price) => fmtCost(v, r.currency === 'CNY' ? '¥' : '$') },
    { title: '缓存读 / 1M', dataIndex: 'cacheReadPerM', width: 130, render: (v: number, r: Price) => fmtCost(v, r.currency === 'CNY' ? '¥' : '$') },
    { title: '缓存写 / 1M', dataIndex: 'cacheWritePerM', width: 130, render: (v: number, r: Price) => fmtCost(v, r.currency === 'CNY' ? '¥' : '$') },
    { title: '币种', dataIndex: 'currency', width: 80, render: (v: string) => <Tag>{v}</Tag> },
    { title: '备注', dataIndex: 'note', render: (v: string) => <span className="muted">{v || '-'}</span> },
    {
      title: '操作', width: 100,
      render: (_: any, r: Price) => (
        <Space size={2}>
          <Button size="small" type="text" icon={<EditOutlined />} title="编辑价格" onClick={() => openEdit(r)} />
          <Popconfirm title="删除该价格？" onConfirm={() => del.mutate(r.id)}>
            <Button size="small" type="text" danger icon={<DeleteOutlined />} title="删除价格" />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="模型定价"
        extra={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建价格</Button>}
      />
      <div className="panel">
        <Table rowKey="id" loading={isLoading} dataSource={data} columns={cols as any} pagination={{ pageSize: 20 }} />
      </div>
      <Modal open={open} title={editing ? '编辑价格' : '新建价格'} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={save.isPending} destroyOnClose>
        <Form form={form} layout="vertical" onFinish={v => save.mutate(v)}>
          <Form.Item name="pattern" label="模型匹配" rules={[{ required: true, message: '请输入模型匹配模式' }]}>
            <Input placeholder="gpt-4o* 或 gemini-2.5-flash" />
          </Form.Item>
          <Space style={{ width: '100%' }} align="start">
            <Form.Item name="inputPerM" label="输入 / 1M" style={{ width: 150 }}><InputNumber min={0} step={0.01} style={{ width: '100%' }} /></Form.Item>
            <Form.Item name="outputPerM" label="输出 / 1M" style={{ width: 150 }}><InputNumber min={0} step={0.01} style={{ width: '100%' }} /></Form.Item>
            <Form.Item name="cacheReadPerM" label="缓存读 / 1M" style={{ width: 150 }}><InputNumber min={0} step={0.01} style={{ width: '100%' }} /></Form.Item>
            <Form.Item name="cacheWritePerM" label="缓存写 / 1M" style={{ width: 150 }}><InputNumber min={0} step={0.01} style={{ width: '100%' }} /></Form.Item>
            <Form.Item name="currency" label="币种" style={{ width: 100 }}>
              <Input />
            </Form.Item>
          </Space>
          <Form.Item name="note" label="备注"><Input /></Form.Item>
        </Form>
      </Modal>
    </>
  )
}
