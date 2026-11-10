import { useQuery } from '@tanstack/react-query'
import { Descriptions, Drawer, Table, Tabs, Tag, Typography } from 'antd'
import { api } from '../api'
import type { SnapshotDetail } from '../types'
import { EmptyPanel, JsonBlock, SeverityTag, CopyButton } from './common'
import { categoryLabel, fmtBytes, fmtTime } from '../format'

export default function SnapshotDrawer({ id, open, onClose }: { id?: number; open: boolean; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ['snapshot', id],
    queryFn: () => api.snapshot(id!),
    enabled: open && !!id,
  })
  const sn: SnapshotDetail | undefined = data?.item
  return (
    <Drawer open={open} onClose={onClose} width={1000} title={sn ? `请求快照 #${sn.id} · ${sn.caseName}` : '请求快照'} destroyOnClose>
      {!sn ? <EmptyPanel text={isLoading ? '加载中…' : '无数据'} /> : (
        <>
          <Descriptions size="small" bordered column={2} style={{ marginBottom: 12 }}>
            <Descriptions.Item label="运行">{sn.runId}</Descriptions.Item>
            <Descriptions.Item label="用例">{sn.caseName || '-'}（{sn.caseId}）</Descriptions.Item>
            <Descriptions.Item label="目标">{sn.targetLabel} / {sn.targetName}</Descriptions.Item>
            <Descriptions.Item label="模型"><span className="mono">{sn.model}</span></Descriptions.Item>
            <Descriptions.Item label="协议">{sn.protocol}</Descriptions.Item>
            <Descriptions.Item label="重复">{sn.repetition}</Descriptions.Item>
            <Descriptions.Item label="请求时间">{fmtTime(sn.requestTimeMs)}</Descriptions.Item>
            <Descriptions.Item label="请求 ID"><span className="small-mono">{sn.requestId}</span></Descriptions.Item>
            <Descriptions.Item label="端点" span={2}><span className="small-mono">{sn.endpoint}</span></Descriptions.Item>
            <Descriptions.Item label="正文大小">{fmtBytes(sn.bodySize)}</Descriptions.Item>
            <Descriptions.Item label="正文 SHA-256" span={1}><span className="small-mono">{sn.bodySha256}</span></Descriptions.Item>
            <Descriptions.Item label="隐藏指令线索" span={2}>
              {sn.findings.length === 0 ? <Tag color="green">未发现</Tag> : <Tag color="red">{sn.findings.length} 条</Tag>}
            </Descriptions.Item>
          </Descriptions>

          <div className="panel" style={{ marginBottom: 12 }}>
            <div className="panel-title">分区指纹（SHA-256）</div>
            <Table
              size="small"
              rowKey="key"
              pagination={false}
              dataSource={Object.entries(sn.fingerprints || {}).filter(([, v]) => !!v).map(([k, v]) => ({ key: k, value: v }))}
              columns={[
                { title: '分区', dataIndex: 'key', width: 180, render: (v: string) => categoryLabel(v) },
                { title: 'SHA-256', dataIndex: 'value', render: (v: string) => <span className="small-mono">{v}</span> },
              ]}
            />
            {!Object.keys(sn.fingerprints || {}).length && <span className="muted">无</span>}
          </div>

          <Tabs
            items={[
              {
                key: 'findings', label: `隐藏指令线索 (${sn.findings.length})`,
                children: sn.findings.length ? (
                  <Table
                    size="small"
                    rowKey={(_, i) => String(i)}
                    pagination={{ pageSize: 20 }}
                    dataSource={sn.findings}
                    columns={[
                      { title: '级别', dataIndex: 'severity', width: 80, render: (v: string) => <SeverityTag severity={v} /> },
                      { title: '分类', dataIndex: 'category', width: 130, render: (v: string) => <Tag>{categoryLabel(v)}</Tag> },
                      { title: '规则', dataIndex: 'rule', width: 150 },
                      { title: 'JSONPath', dataIndex: 'path', width: 220, render: (v: string) => <span className="small-mono">{v}</span> },
                      { title: '证据', dataIndex: 'snippet', render: (v: string) => <Typography.Text style={{ fontSize: 12 }}>{v}</Typography.Text> },
                      { title: '说明', dataIndex: 'detail', render: (v: string) => <span className="muted">{v}</span> },
                    ]}
                  />
                ) : <EmptyPanel text="未命中隐藏指令规则" />,
              },
              { key: 'wire', label: 'Wire 请求 JSON', children: <JsonBlock value={sn.body} maxHeight={620} /> },
              { key: 'canonical', label: 'Canonical 请求 JSON', children: <JsonBlock value={sn.canonical} maxHeight={620} /> },
            ]}
          />
          <div style={{ marginTop: 8 }}>
            <CopyButton text={sn.body} label="复制 Wire JSON" />
            <CopyButton text={sn.canonical} label="复制 Canonical JSON" />
          </div>
        </>
      )}
    </Drawer>
  )
}
