import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Popconfirm, Space, Table, Tag, message } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Target } from '../types'
import { PageHeader, ProtocolTag } from '../components/common'
import TargetFormModal from '../components/TargetFormModal'
import { fmtTime } from '../format'

export default function Targets() {
  const qc = useQueryClient()
  const { data: targets = [], isLoading } = useQuery({ queryKey: ['targets'], queryFn: api.targets, select: d => d.items })
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Target | null>(null)

  const openCreate = () => {
    setEditing(null)
    setModalOpen(true)
  }
  const openEdit = (t: Target) => {
    setEditing(t)
    setModalOpen(true)
  }

  const del = useMutation({
    mutationFn: (id: string) => api.deleteTarget(id),
    onSuccess: () => {
      message.success('已删除')
      qc.invalidateQueries({ queryKey: ['targets'] })
    },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })

  const fetchModels = useMutation({
    mutationFn: (id: string) => api.targetModels(id),
    onSuccess: (res: any) => {
      message.success(`已获取 ${res.models?.length || 0} 个模型`)
      qc.invalidateQueries({ queryKey: ['targets'] })
      qc.invalidateQueries({ queryKey: ['models'] })
    },
    onError: (e: any) => message.error(e?.message || '获取模型失败', 8),
  })

  const columns = [
    {
      title: '名称', dataIndex: 'name', width: 180,
      render: (v: string) => <b>{v}</b>,
    },
    {
      title: 'Base URL', dataIndex: 'baseUrl',
      render: (v: string) => <span className="small-mono">{v}</span>,
    },
    {
      title: '协议', dataIndex: 'protocol', width: 110,
      render: (v: string) => <ProtocolTag protocol={v} />,
    },
    {
      title: 'API Key', dataIndex: 'keyMasked', width: 170,
      render: (v: string) => v ? <Tag color="green">{v}</Tag> : <Tag>未配置</Tag>,
    },
    {
      title: '模型数', dataIndex: 'modelCount', width: 90,
      render: (v: number) => v ? <Tag color="blue">{v}</Tag> : <span className="muted">-</span>,
    },
    {
      title: '最近检测', dataIndex: 'lastTestedAt', width: 170,
      render: (v: number) => <span className="muted">{fmtTime(v)}</span>,
    },
    {
      title: '操作', key: 'actions', width: 220,
      render: (_: any, r: Target) => (
        <Space size={4}>
          <Button size="small" icon={<ReloadOutlined />} loading={fetchModels.isPending} onClick={() => fetchModels.mutate(r.id)}>取模型</Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          <Popconfirm title="删除该上游？" description="不会删除历史测试记录。" onConfirm={() => del.mutate(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="上游 / 供应商"
        subtitle="保存 New API 或其他 OpenAI 兼容网关的地址与密钥；密钥使用本机 AES-256-GCM 加密存储，接口只返回掩码。"
        extra={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>添加上游</Button>}
      />
      <div className="panel">
        <Table
          rowKey="id"
          loading={isLoading}
          dataSource={targets}
          columns={columns as any}
          pagination={false}
          locale={{ emptyText: '还没有上游，点击右上角添加。' }}
        />
      </div>

      <TargetFormModal
        open={modalOpen}
        target={editing}
        onClose={() => setModalOpen(false)}
      />
    </>
  )
}
