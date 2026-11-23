import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Button, Drawer, Input, Popconfirm, Space, Switch, Table, Tag, message,
} from 'antd'
import { EyeOutlined, FileDoneOutlined, InboxOutlined, ReloadOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { AuthenticityTemplate } from '../types'
import { PageHeader, JsonBlock } from '../components/common'
import { fmtTime } from '../format'

const statusColor: Record<string, string> = { active: 'green', draft: 'gold', archived: 'default' }
const statusLabel: Record<string, string> = { active: '已发布', draft: '草稿', archived: '已归档' }

export default function AuthenticityTemplates() {
  const qc = useQueryClient()
  const [probeId, setProbeId] = useState('')
  const [includeArchived, setIncludeArchived] = useState(true)
  const [detail, setDetail] = useState<AuthenticityTemplate>()

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['authenticityTemplates', probeId, includeArchived],
    queryFn: () => api.authenticityTemplates({ probeId: probeId || undefined, includeArchived: includeArchived ? 1 : undefined }),
  })
  const items = (data?.items || []) as AuthenticityTemplate[]

  const publish = useMutation({
    mutationFn: (id: string) => api.publishAuthenticityTemplate(id),
    onSuccess: () => { message.success('已发布，后续真实性测试立即使用该版本'); qc.invalidateQueries({ queryKey: ['authenticityTemplates'] }) },
    onError: (e: any) => message.error(e?.message || '发布失败'),
  })
  const archive = useMutation({
    mutationFn: (id: string) => api.archiveAuthenticityTemplate(id),
    onSuccess: () => { message.success('已归档'); qc.invalidateQueries({ queryKey: ['authenticityTemplates'] }) },
    onError: (e: any) => message.error(e?.message || '归档失败'),
  })

  const grouped = useMemo(() => {
    const map = new Map<string, AuthenticityTemplate[]>()
    for (const t of items) {
      const key = `${t.probeId} · ${t.provider || 'all'}`
      const arr = map.get(key) || []
      arr.push(t)
      map.set(key, arr)
    }
    return Array.from(map.entries())
  }, [items])

  return (
    <>
      <PageHeader
        title={<Space><FileDoneOutlined /> 真实性用例</Space>}
        extra={[
          <Input key="probe" allowClear placeholder="按 probeId 过滤" value={probeId} onChange={e => setProbeId(e.target.value)} style={{ width: 260 }} />,
          <Space key="arch" size={4}><Switch checked={includeArchived} onChange={setIncludeArchived} /> 显示已归档</Space>,
          <Button key="refresh" icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>刷新</Button>,
        ]}
      />
      {grouped.length === 0 && !isLoading && (
        <div className="panel"><InboxOutlined /> 暂无模板</div>
      )}
      {grouped.map(([key, list]) => (
        <div className="panel" key={key}>
          <div className="panel-title">{key}</div>
          <Table
            size="small"
            rowKey="id"
            pagination={false}
            dataSource={list}
            columns={[
              { title: '版本', dataIndex: 'version', width: 80, render: (v: number) => <Tag>v{v}</Tag> },
              {
                title: '状态', dataIndex: 'status', width: 100,
                render: (v: string) => <Tag color={statusColor[v] || 'default'}>{statusLabel[v] || v}</Tag>,
              },
              { title: '名称', dataIndex: 'name', width: 220 },
              { title: '说明', dataIndex: 'note', ellipsis: true },
              { title: '来源', width: 210, render: (_: any, r: AuthenticityTemplate) => r.sourceRunId ? <span className="small-mono">{r.sourceRunId} #{r.sourceItemId || '-'}</span> : '-' },
              { title: '创建时间', dataIndex: 'createdAt', width: 170, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
              {
                title: '操作', width: 250,
                render: (_: any, r: AuthenticityTemplate) => (
                  <Space size={2}>
                    <Button size="small" type="link" icon={<EyeOutlined />} onClick={() => setDetail(r)}>查看</Button>
                    {r.status !== 'active' && (
                      <Popconfirm
                        title={`发布 ${r.name} v${r.version}？`}
                        description="发布会归档同 probe/provider 的旧 active 版本，并立即影响后续真实性测试。"
                        onConfirm={() => publish.mutate(r.id)}
                      >
                        <Button size="small" type="link">发布</Button>
                      </Popconfirm>
                    )}
                    {r.status !== 'archived' && (
                      <Popconfirm title="归档该版本？" description="归档不会删除历史，可随时查看。" onConfirm={() => archive.mutate(r.id)}>
                        <Button size="small" type="link" danger>归档</Button>
                      </Popconfirm>
                    )}
                  </Space>
                ),
              },
            ]}
          />
        </div>
      ))}

      <Drawer
        open={!!detail}
        onClose={() => setDetail(undefined)}
        width={960}
        title={detail ? `${detail.name} · v${detail.version} · ${statusLabel[detail.status] || detail.status}` : '模板详情'}
        destroyOnClose
      >
        {detail && (
          <div>
            <Alert type="info" showIcon style={{ marginBottom: 12 }} message={`Probe: ${detail.probeId} · Provider: ${detail.provider || 'all'}`} description={detail.note || '无说明'} />
            <h3>期望 Spec</h3>
            <JsonBlock value={detail.spec} maxHeight={260} />
            <h3>样本请求</h3>
            {detail.sampleRequest ? <JsonBlock value={detail.sampleRequest} maxHeight={320} /> : <p className="muted">未保存样本请求</p>}
            <h3>样本响应</h3>
            {detail.sampleResponse ? <JsonBlock value={detail.sampleResponse} maxHeight={420} /> : <p className="muted">未保存样本响应</p>}
          </div>
        )}
      </Drawer>
    </>
  )
}
