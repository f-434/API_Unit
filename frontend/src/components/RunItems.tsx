import ScrollTable from './ScrollTable'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '../pagination'
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Alert, Button, Descriptions, Drawer, Image, Input, Select, Space, Switch, Tabs, Tag, Tooltip, Typography,
} from 'antd'
import { EyeOutlined, SearchOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Item, ItemDetail } from '../types'
import {
  errorTypeLabel, fmtCost, fmtInt, fmtMs, fmtTime, tokenSourceLabel, truncate, verdictColor, verdictLabel,
} from '../format'
import { CopyButton, EmptyPanel, JsonBlock, ResponseModelCell, VerdictTag } from './common'

function actualModel(it: Item): string | undefined {
  if (it.responseModel) return it.responseModel
  const legacy = it.meta?.upstreamModel
  return typeof legacy === 'string' ? legacy : undefined
}

function statusTag(it: Item) {
  const authenticity = it.meta?.authenticity
  const checkStatus = it.meta?.checkStatus
  if (authenticity && checkStatus === 'pass' && it.status >= 400) {
    return <Tag color="orange">{it.status}（预期）</Tag>
  }
  if (authenticity && checkStatus === 'fail') {
    return <Tag color="red">{it.status || '失败'}</Tag>
  }
  if (it.status === 0) return <Tag color="default">网络/无响应</Tag>
  if (it.status < 200 || it.status >= 300) return <Tag color="red">{it.status}</Tag>
  if (it.ok) return <Tag color="green">{it.status}</Tag>
  // 2xx but semantically empty/interrupted
  return <Tag color="orange">{it.status}</Tag>
}

export function ItemDrawer({ runId, itemId, open, onClose }: { runId: string; itemId?: number; open: boolean; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ['item', runId, itemId],
    queryFn: () => api.item(runId, itemId!),
    enabled: open && !!itemId,
  })
  const it: ItemDetail | undefined = data?.item
  return (
    <Drawer open={open} onClose={onClose} width={980} title={it ? `请求结果 #${it.index + 1} · ${it.caseName || it.model}` : '请求详情'} destroyOnClose>
      {!it ? <EmptyPanel text={isLoading ? '加载中…' : '无数据'} /> : (
        <>
          <Descriptions size="small" bordered column={2} style={{ marginBottom: 14 }}>
            <Descriptions.Item label="模型"><span className="mono">{it.model}</span></Descriptions.Item>
            <Descriptions.Item label="响应模型"><ResponseModelCell requested={it.model} models={[actualModel(it)]} /></Descriptions.Item>
            <Descriptions.Item label="上游">{it.targetName || '-'}</Descriptions.Item>
            <Descriptions.Item label="用例">{it.caseName || '-'}</Descriptions.Item>
            <Descriptions.Item label="分类">{it.category || '-'}</Descriptions.Item>
            <Descriptions.Item label="HTTP / 结果">{statusTag(it)} {it.ok ? '成功' : '失败'}</Descriptions.Item>
            <Descriptions.Item label="判定"><VerdictTag verdict={it.verdict} /></Descriptions.Item>
            <Descriptions.Item label="延迟">{fmtMs(it.latencyMs)}</Descriptions.Item>
            <Descriptions.Item label="TTFT">{fmtMs(it.ttftMs)}</Descriptions.Item>
            <Descriptions.Item label="生成耗时">{fmtMs(it.generationMs)}</Descriptions.Item>
            <Descriptions.Item label="TPOT">{fmtMs(it.tpotMs)}</Descriptions.Item>
            <Descriptions.Item label="输入 Token">{fmtInt(it.promptTokens)} <Tag>{tokenSourceLabel(it.tokenSource)}</Tag></Descriptions.Item>
            <Descriptions.Item label="本地分词器">{fmtInt(it.localPromptTokens)}</Descriptions.Item>
            <Descriptions.Item label="输出 Token">{fmtInt(it.completionTokens)}</Descriptions.Item>
            <Descriptions.Item label="缓存 Token">{fmtInt(it.cachedTokens)} / 写 {fmtInt(it.cacheWriteTokens)}</Descriptions.Item>
            <Descriptions.Item label="推理 Token">{fmtInt(it.reasoningTokens)}</Descriptions.Item>
            <Descriptions.Item label="成本">{fmtCost(it.cost)}</Descriptions.Item>
            <Descriptions.Item label="尝试次数">{it.attempts}</Descriptions.Item>
            <Descriptions.Item label="结束原因">{it.finishReason || '-'}</Descriptions.Item>
            <Descriptions.Item label="请求 ID" span={2}>
              <Space size={4}>
                <span className="small-mono">{it.requestId || '-'}</span>
                {it.requestId && <CopyButton text={it.requestId} />}
              </Space>
            </Descriptions.Item>
            <Descriptions.Item label="开始时间" span={2}>{fmtTime(it.startedAt)}</Descriptions.Item>
            {it.errorMessage && (
              <Descriptions.Item label="错误" span={2}>
                <Typography.Text type="danger">{it.errorType ? `[${errorTypeLabel(it.errorType)}] ` : ''}{it.errorMessage}</Typography.Text>
              </Descriptions.Item>
            )}
          </Descriptions>
          {it.judge && (
            <div className="panel" style={{ marginBottom: 14 }}>
              <div className="panel-title">裁判结果 <Tag color={verdictColor(it.judge.verdict)}>{verdictLabel(it.judge.verdict)}</Tag> <Tag>{it.judge.method}</Tag></div>
              <div className="muted">score {it.judge.score} · tokens {it.judge.tokens ?? '-'} · cost {fmtCost(it.judge.cost)} {it.judge.model ? `· ${it.judge.model}` : ''}</div>
              <p style={{ marginTop: 8, whiteSpace: 'pre-wrap' }}>{it.judge.reason || '-'}</p>
              {it.judge.raw && <JsonBlock title="裁判原始输出" value={it.judge.raw} maxHeight={240} />}
            </div>
          )}
          {!!it.checks?.length && (
            <div className="panel" style={{ marginBottom: 14 }}>
              <div className="panel-title">确定性检查</div>
              <ScrollTable
                size="small"
                rowKey={(_, i) => String(i)}
                pagination={false}
                dataSource={it.checks}
                columns={[
                  { title: '类型', dataIndex: ['check', 'kind'], width: 130 },
                  { title: '期望值', dataIndex: ['check', 'value'], render: (v: string) => <span className="small-mono">{v || '-'}</span> },
                  { title: '命中', dataIndex: 'matched', width: 80, render: (v: boolean) => v ? <Tag color="red">命中</Tag> : <Tag>未命中</Tag> },
                  { title: '证据', dataIndex: 'evidence', render: (v: string) => <span className="small-mono">{truncate(v, 180) || '-'}</span> },
                ]}
              />
            </div>
          )}
          {!!it.meta?.imagePath && (
            <div className="panel" style={{ marginBottom: 14 }}>
              <div className="panel-title">生成图片</div>
              <Space align="start" wrap>
                <Image
                  src={String(it.meta.imagePath)}
                  width={280}
                  style={{ borderRadius: 8, border: '1px solid #e5e6eb' }}
                />
                <div className="muted" style={{ fontSize: 12, lineHeight: 1.8 }}>
                  <div>格式：{it.meta.imageFormat || '-'}</div>
                  <div>尺寸：{it.meta.imageWidth || '-'} × {it.meta.imageHeight || '-'}</div>
                  <div className="small-mono" style={{ wordBreak: 'break-all' }}>{String(it.meta.imagePath)}</div>
                </div>
              </Space>
            </div>
          )}
          {it.meta?.truncated && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 10 }}
              message="正文已截断"
              description="保存上限：请求 8MB / 响应 1MB / 输出文本 256KB。超过上限的部分不会写入数据库。"
            />
          )}
          <Tabs
            items={[
              {
                key: 'out', label: '输出文本',
                children: (
                  <div>
                    <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 4 }}>
                      <CopyButton text={it.outputText || it.outputPreview || ''} />
                    </div>
                    <pre className="json-block" style={{ maxHeight: 420, padding: 12, background: '#0f172a', color: '#e2e8f0', borderRadius: 10, overflow: 'auto', whiteSpace: 'pre-wrap' }}>{it.outputText || it.outputPreview || '-'}</pre>
                  </div>
                ),
              },
              {
                key: 'req', label: '请求 JSON',
                children: it.requestJson
                  ? <JsonBlock value={it.requestJson} maxHeight={440} />
                  : <Alert type="info" showIcon message="未捕获请求正文" description="创建任务时需开启“保存请求/响应正文”。" />,
              },
              {
                key: 'resp', label: '响应 JSON',
                children: it.responseJson
                  ? <JsonBlock value={it.responseJson} maxHeight={440} />
                  : <Alert type="info" showIcon message="未捕获响应正文" description="创建任务时需开启“保存请求/响应正文”；流式响应最多保留 1MB 原文。" />,
              },
              { key: 'meta', label: 'Meta', children: <JsonBlock value={it.meta || {}} maxHeight={440} /> },
            ]}
          />
          <div style={{ marginTop: 8 }}>
            <CopyButton text={JSON.stringify(it, null, 2)} label="复制全部字段" />
          </div>
        </>
      )}
    </Drawer>
  )
}

export default function RunItems({ runId, kind, models = [], polling = false }: { runId: string; kind: string; models?: string[]; polling?: boolean }) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [model, setModel] = useState<string>()
  const [onlyFail, setOnlyFail] = useState(false)
  const [verdict, setVerdict] = useState<string>()
  const [q, setQ] = useState('')
  const [detailId, setDetailId] = useState<number>()
  const [detailOpen, setDetailOpen] = useState(false)

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['items', runId, page, pageSize, model, onlyFail, verdict, q],
    queryFn: () => api.items(runId, {
      limit: pageSize, offset: (page - 1) * pageSize, model,
      onlyFail: onlyFail ? 1 : undefined, verdict, q: q || undefined,
    }),
    refetchInterval: polling ? 3000 : false,
  })

  const openDetail = (it: Item) => { setDetailId(it.id); setDetailOpen(true) }

  const columns: any[] = [
    { title: '#', dataIndex: 'index', width: 58, render: (_: any, it: Item) => <span className="muted">{it.index + 1}</span> },
    ...(kind === 'authenticity' ? [{ title: '轮次', dataIndex: 'repetition', width: 70, render: (v: number) => v ? <Tag>第 {v} 次</Tag> : '-' }] : []),
    { title: '模型', dataIndex: 'model', width: 190, render: (v: string) => <span className="mono">{v}</span> },
    {
      title: '响应模型', key: 'responseModel', width: 200,
      render: (_: any, it: Item) => <ResponseModelCell requested={it.model} models={[actualModel(it)]} />,
    },
    { title: '上游', dataIndex: 'targetName', width: 120, render: (v: string) => v || '-' },
    ...(kind !== 'stress' ? [{ title: '用例', dataIndex: 'caseName', width: 170 }] : []),
    ...(kind === 'injection' ? [{ title: '判定', dataIndex: 'verdict', width: 100, render: (v: string) => <VerdictTag verdict={v} /> }] : []),
    ...(kind === 'authenticity' ? [{
      title: '检查', dataIndex: 'verdict', width: 90,
      render: (v: string) => {
        const color = v === 'pass' ? 'green' : v === 'fail' ? 'red' : v === 'warn' ? 'gold' : 'default'
        const label = v === 'pass' ? '通过' : v === 'fail' ? '失败' : v === 'warn' ? '警告' : 'N/A'
        return <Tag color={color}>{label}</Tag>
      },
    }] : []),
    { title: '状态', width: 105, render: (_: any, it: Item) => statusTag(it) },
    { title: '延迟', dataIndex: 'latencyMs', width: 90, render: (v: number) => fmtMs(v) },
    { title: 'TTFT', dataIndex: 'ttftMs', width: 90, render: (v: number) => fmtMs(v) },
    { title: '输入/输出', width: 120, render: (_: any, it: Item) => <span>{fmtInt(it.promptTokens)} / {fmtInt(it.completionTokens)}</span> },
    { title: '来源', dataIndex: 'tokenSource', width: 85, render: (v: string) => <Tag>{tokenSourceLabel(v)}</Tag> },
    { title: '成本', dataIndex: 'cost', width: 90, render: (v: number) => fmtCost(v) },
    {
      title: '错误/预览', key: 'err', width: 300, ellipsis: { showTitle: false },
      render: (_: any, it: Item) => it.ok ? (
        <Tooltip placement="topLeft" title={<div style={{ whiteSpace: 'pre-wrap' }}>{it.outputPreview || '-'}</div>}>
          <span className="muted">{truncate(it.outputPreview, 120) || '-'}</span>
        </Tooltip>
      ) : (
        <Tooltip placement="topLeft" title={<div style={{ whiteSpace: 'pre-wrap' }}>{it.errorMessage || '-'}</div>}>
          <span style={{ color: '#dc2626' }}>{it.errorType ? errorTypeLabel(it.errorType) : '失败'}: {truncate(it.errorMessage, 120)}</span>
        </Tooltip>
      ),
    },
    { title: '测试时间', dataIndex: 'startedAt', width: 150, render: (v: number) => <span className="muted">{fmtTime(v)}</span> },
    { title: '', width: 48, render: (_: any, it: Item) => <Button size="small" type="text" icon={<EyeOutlined />} title="查看详情" onClick={() => openDetail(it)} /> },
  ]

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select allowClear placeholder="全部模型" value={model} onChange={v => { setModel(v); setPage(1) }} style={{ width: 220 }} options={models.map(m => ({ value: m, label: m }))} showSearch />
        {kind === 'injection' && (
          <Select allowClear placeholder="全部判定" value={verdict} onChange={v => { setVerdict(v); setPage(1) }} style={{ width: 140 }}
            options={['success', 'blocked', 'unclear', 'error'].map(v => ({ value: v, label: verdictLabel(v) }))} />
        )}
        <Space size={6}><Switch checked={onlyFail} onChange={v => { setOnlyFail(v); setPage(1) }} /> 只看失败</Space>
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索用例/模型/错误" value={q} onChange={e => { setQ(e.target.value); setPage(1) }} style={{ width: 240 }} />
        <Tag>{data?.total ?? 0} 条</Tag>
        {polling && isFetching && <Tag color="processing">更新中</Tag>}
      </Space>
      <ScrollTable
        rowKey="id"
        className="items-table"
        size="small"
        loading={isLoading}
        dataSource={data?.items || []}
        columns={columns}
        scroll={{ x: 1840 }}
        pagination={{
          current: page, pageSize, total: data?.total || 0,
          showSizeChanger: true, pageSizeOptions: PAGE_SIZE_OPTIONS,
          onChange: (p, ps) => { setPage(p); setPageSize(ps) },
        }}
        onRow={it => ({ onDoubleClick: () => openDetail(it), style: { cursor: 'pointer' } })}
      />
      <ItemDrawer runId={runId} itemId={detailId} open={detailOpen} onClose={() => setDetailOpen(false)} />
    </div>
  )
}
