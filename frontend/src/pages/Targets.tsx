import { useMemo, useState, type Key } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Input, Popconfirm, Space, Table, Tag, message } from 'antd'
import {
  CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined, SearchOutlined,
} from '@ant-design/icons'
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
  const [q, setQ] = useState('')
  const [selectedKeys, setSelectedKeys] = useState<Key[]>([])
  const [fetchingId, setFetchingId] = useState<string | null>(null)
  const [bulkFetching, setBulkFetching] = useState(false)
  const [bulkDone, setBulkDone] = useState(0)

  const openCreate = () => {
    setEditing(null)
    setModalOpen(true)
  }
  const openEdit = (t: Target) => {
    setEditing(t)
    setModalOpen(true)
  }

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    if (!s) return targets
    return targets.filter(t => t.name.toLowerCase().includes(s) || t.baseUrl.toLowerCase().includes(s))
  }, [targets, q])

  const refreshQueries = () => {
    qc.invalidateQueries({ queryKey: ['targets'] })
    qc.invalidateQueries({ queryKey: ['models'] })
  }

  const del = useMutation({
    mutationFn: (id: string) => api.deleteTarget(id),
    onSuccess: (_res, id) => {
      message.success('已删除')
      setSelectedKeys(prev => prev.filter(k => k !== id))
      refreshQueries()
    },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })

  const delSelected = useMutation({
    mutationFn: async (ids: string[]) => {
      const results = await Promise.allSettled(ids.map(id => api.deleteTarget(id)))
      const ok = results.filter(r => r.status === 'fulfilled').length
      return { ok, failed: results.length - ok }
    },
    onSuccess: ({ ok, failed }) => {
      if (ok) message.success(`已删除 ${ok} 个上游`)
      if (failed) message.error(`${failed} 个上游删除失败`)
      setSelectedKeys([])
      refreshQueries()
    },
    onError: (e: any) => message.error(e?.message || '批量删除失败'),
  })

  // 单行取模型：只让当前行显示加载动画。
  const fetchOne = async (id: string) => {
    if (fetchingId || bulkFetching) return
    setFetchingId(id)
    try {
      const res = await api.targetModels(id)
      message.success(`已获取 ${res.models?.length || 0} 个模型`)
      refreshQueries()
    } catch (e: any) {
      message.error(e?.message || '获取模型失败', 8)
    } finally {
      setFetchingId(null)
    }
  }

  // 批量取模型：逐个获取，当前正在获取的行显示加载动画。
  const fetchAllModels = async () => {
    if (bulkFetching || fetchingId || !targets.length) return
    setBulkFetching(true)
    setBulkDone(0)
    let ok = 0
    let failed = 0
    for (let i = 0; i < targets.length; i++) {
      setFetchingId(targets[i].id)
      try {
        await api.targetModels(targets[i].id)
        ok++
      } catch {
        failed++
      }
      setBulkDone(i + 1)
    }
    setFetchingId(null)
    setBulkFetching(false)
    refreshQueries()
    if (failed) message.warning(`获取完成：成功 ${ok} 个，失败 ${failed} 个`, 8)
    else message.success(`已获取 ${ok} 个渠道的模型`)
  }

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
          <Button
            size="small"
            icon={<ReloadOutlined />}
            loading={fetchingId === r.id}
            disabled={bulkFetching || (!!fetchingId && fetchingId !== r.id)}
            onClick={() => fetchOne(r.id)}
          >
            取模型
          </Button>
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
        extra={[
          <Input
            key="q"
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索名称或 Base URL"
            value={q}
            onChange={e => {
              setQ(e.target.value)
              // 清空选择，避免批量删除时误删被搜索条件隐藏的行。
              setSelectedKeys([])
            }}
            style={{ width: 240 }}
          />,
          <Popconfirm
            key="bulk-delete"
            title={`删除选中的 ${selectedKeys.length} 个上游？`}
            description="不会删除历史测试记录。"
            disabled={!selectedKeys.length}
            onConfirm={() => delSelected.mutate(selectedKeys as string[])}
          >
            <Button
              danger
              icon={<DeleteOutlined />}
              disabled={!selectedKeys.length}
              loading={delSelected.isPending}
            >
              批量删除{selectedKeys.length ? `（${selectedKeys.length}）` : ''}
            </Button>
          </Popconfirm>,
          <Button
            key="fetch-all"
            icon={<CloudDownloadOutlined />}
            loading={bulkFetching}
            disabled={!targets.length}
            onClick={fetchAllModels}
          >
            {bulkFetching ? `获取中 ${bulkDone}/${targets.length}` : '获取所有渠道模型'}
          </Button>,
          <Button key="add" type="primary" icon={<PlusOutlined />} onClick={openCreate}>添加上游</Button>,
        ]}
      />
      <div className="panel">
        <Table
          rowKey="id"
          loading={isLoading}
          dataSource={filtered}
          columns={columns as any}
          pagination={false}
          rowSelection={{ selectedRowKeys: selectedKeys, onChange: keys => setSelectedKeys(keys) }}
          locale={{ emptyText: q.trim() ? '没有匹配的上游。' : '还没有上游，点击右上角添加。' }}
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
