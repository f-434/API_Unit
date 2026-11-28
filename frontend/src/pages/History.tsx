import ScrollTable from '../components/ScrollTable'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '../pagination'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Popconfirm, Select, Space, Tag, message } from 'antd'
import { DeleteOutlined, EyeOutlined, ReloadOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { RunRow } from '../types'
import { KindTag, PageHeader, StatusTag, TargetTags } from '../components/common'
import { fmtDuration, fmtPct, fmtTime } from '../format'

export default function History() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [kind, setKind] = useState<string>()
  const [status, setStatus] = useState<string>()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['runs', kind, status, page, pageSize],
    queryFn: () => api.runs({ kind, status, limit: pageSize, offset: (page - 1) * pageSize }),
    refetchOnMount: 'always',
    refetchInterval: (query) => {
      const items = (query.state.data as any)?.items as RunRow[] | undefined
      return items?.some(r => r.status === 'running' || r.status === 'queued') ? 4000 : false
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => api.deleteRun(id),
    onSuccess: () => { message.success('已删除'); qc.invalidateQueries({ queryKey: ['runs'] }) },
    onError: (e: any) => message.error(e?.message || '删除失败'),
  })

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 190, render: (v: string) => <span className="small-mono">{v}</span> },
    { title: '类型', dataIndex: 'kind', width: 125, render: (v: string) => <KindTag kind={v} /> },
    { title: '上游', dataIndex: ['summary', 'targets'], width: 220, render: (v: any) => <TargetTags targets={v} /> },
    { title: '状态', dataIndex: 'status', width: 95, render: (v: string) => <StatusTag status={v} /> },
    { title: '模型', dataIndex: ['summary', 'models'], render: (v: string[]) => (v || []).slice(0, 3).map(m => <Tag key={m}>{m}</Tag>) },
    { title: '请求', width: 85, render: (_: any, r: RunRow) => r.summary?.total ?? 0 },
    { title: '成功率', width: 95, render: (_: any, r: RunRow) => fmtPct(r.summary?.successRate) },
    { title: 'QPS / ASR', width: 100, render: (_: any, r: RunRow) => r.kind === 'injection' ? fmtPct(r.summary?.asr) : (r.summary?.qps ?? 0).toFixed(2) },
    { title: 'RPM', width: 85, render: (_: any, r: RunRow) => r.kind === 'stress' ? (r.summary?.rpm ?? 0).toFixed(2) : '-' },
    { title: '耗时', width: 100, render: (_: any, r: RunRow) => fmtDuration(r.summary?.elapsedMs || r.durationMs) },
    { title: '创建时间', dataIndex: 'createdAt', width: 170, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
    {
      title: '操作', width: 110,
      render: (_: any, r: RunRow) => (
        <Space size={2}>
          <Button size="small" type="text" icon={<EyeOutlined />} onClick={e => { e.stopPropagation(); navigate(`/runs/${r.id}`) }} />
          <Popconfirm title="删除该测试记录？" description="结果明细与请求快照将一并删除。" onConfirm={() => del.mutate(r.id)}>
            <Button size="small" type="text" danger icon={<DeleteOutlined />} onClick={e => e.stopPropagation()} />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="测试历史"
        extra={[
          <Select key="kind" allowClear placeholder="全部类型" value={kind} onChange={v => { setKind(v); setPage(1) }} style={{ width: 150 }}
            options={[
              { value: 'stress', label: '并发压测' },
              { value: 'token_audit', label: 'Token 审计' },
              { value: 'request_diff', label: '请求差异审计' },
              { value: 'injection', label: '提示词注入' },
              { value: 'authenticity', label: '模型真实性' },
              { value: 'image_detection', label: '生图检测' },
              { value: 'claude_signature', label: 'signature 签名核验' },
            ]} />,
          <Select key="status" allowClear placeholder="全部状态" value={status} onChange={v => { setStatus(v); setPage(1) }} style={{ width: 130 }}
            options={['running', 'completed', 'stopped', 'failed', 'interrupted'].map(s => ({ value: s, label: s }))} />,
          <Button key="refresh" icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>刷新</Button>,
        ]}
      />
      <div className="panel">
        <ScrollTable
          rowKey="id"
          size="small"
          loading={isLoading}
          dataSource={data?.items || []}
          columns={columns as any}
          scroll={{ x: 1640 }}
          onRow={r => ({ onClick: () => navigate(`/runs/${r.id}`), style: { cursor: 'pointer' } })}
          pagination={{
            current: page, pageSize, total: data?.total || 0,
            showSizeChanger: true, pageSizeOptions: PAGE_SIZE_OPTIONS,
            onChange: (p, ps) => { setPage(p); setPageSize(ps) },
          }}
        />
      </div>
    </>
  )
}
