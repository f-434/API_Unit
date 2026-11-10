import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AutoComplete, Button, Col, Input, Row, Select, Space, Tooltip, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined, ReloadOutlined, WarningOutlined } from '@ant-design/icons'
import { api } from '../api'
import type { Target } from '../types'
import { protocolLabel } from '../format'

export function useTargets() {
  return useQuery({ queryKey: ['targets'], queryFn: api.targets, select: d => d.items })
}

export function useModels(targetId?: string) {
  return useQuery({
    queryKey: ['models', targetId],
    queryFn: () => api.targetModels(targetId!),
    enabled: !!targetId,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}

export function TargetSelect({ value, onChange, targets, placeholder = '选择上游' }: {
  value?: string
  onChange?: (v: string) => void
  targets: Target[]
  placeholder?: string
}) {
  return (
    <Select
      value={value || undefined}
      onChange={onChange}
      placeholder={placeholder}
      showSearch
      optionFilterProp="label"
      options={targets.map(t => ({ value: t.id, label: `${t.name} · ${t.baseUrl}` }))}
      style={{ width: '100%' }}
    />
  )
}

export function ModelAutoComplete({ targetId, value, onChange, placeholder = '输入或选择模型' }: {
  targetId?: string
  value?: string
  onChange?: (v: string) => void
  placeholder?: string
}) {
  const { data, isLoading, isError, error, refetch } = useModels(targetId)
  const models = data?.models || []
  return (
    <Space.Compact style={{ width: '100%' }}>
      <AutoComplete
        value={value}
        onChange={onChange}
        options={models.map((m: any) => ({ value: m.id, label: m.id }))}
        placeholder={targetId ? placeholder : '请先选择上游'}
        disabled={!targetId}
        style={{ width: '100%' }}
        filterOption={(input, option) => String(option?.value || '').toLowerCase().includes(input.toLowerCase())}
      />
      <Tooltip title={isError ? `获取模型失败：${(error as any)?.message || ''}` : '刷新模型列表'}>
        <Button
          icon={isError ? <WarningOutlined /> : <ReloadOutlined />}
          loading={isLoading}
          disabled={!targetId}
          onClick={() => refetch()}
        />
      </Tooltip>
    </Space.Compact>
  )
}

export function ModelMultiSelect({ targetId, value, onChange }: {
  targetId?: string
  value?: string[]
  onChange?: (v: string[]) => void
}) {
  const { data, isLoading, isError, error, refetch } = useModels(targetId)
  const models = data?.models || []
  return (
    <Space direction="vertical" style={{ width: '100%' }} size={4}>
      <Space.Compact style={{ width: '100%' }}>
        <Select
          mode="tags"
          value={value}
          onChange={onChange}
          tokenSeparators={[',', '\n', ' ']}
          options={models.map((m: any) => ({ value: m.id, label: `${m.id}${m.ownedBy ? ` · ${m.ownedBy}` : ''}` }))}
          placeholder={targetId ? '选择或输入模型，可多选' : '请先选择上游'}
          disabled={!targetId}
          style={{ width: '100%' }}
        />
        <Button icon={<ReloadOutlined />} loading={isLoading} disabled={!targetId} onClick={() => refetch()} />
      </Space.Compact>
      {isError && <Typography.Text type="danger" style={{ fontSize: 12 }}>模型列表获取失败：{(error as any)?.message}</Typography.Text>}
    </Space>
  )
}

export interface TargetRow {
  targetId: string
  model: string
  label?: string
  tokenizer?: string
  protocol?: string
}

function TargetRowItem({ row, index, targets, tokenizers, showTokenizer, onChange, onRemove }: {
  row: TargetRow
  index: number
  targets: Target[]
  tokenizers: { id: string; label: string }[]
  showTokenizer: boolean
  onChange: (row: TargetRow) => void
  onRemove: () => void
}) {
  const setTarget = (targetId: string) => {
    const t = targets.find(x => x.id === targetId)
    onChange({
      ...row,
      targetId,
      label: row.label || (t ? `${t.name}` : ''),
      model: row.targetId === targetId ? row.model : '',
    })
  }
  return (
    <div className="case-card">
      <Row gutter={[10, 10]} align="middle">
        <Col xs={24} lg={6}>
          <TargetSelect value={row.targetId} onChange={setTarget} targets={targets} placeholder={`目标 ${index + 1}`} />
        </Col>
        <Col xs={24} lg={6}>
          <ModelAutoComplete targetId={row.targetId} value={row.model} onChange={v => onChange({ ...row, model: v })} />
        </Col>
        <Col xs={12} lg={3}>
          <Input value={row.label} onChange={e => onChange({ ...row, label: e.target.value })} placeholder="对比标签" />
        </Col>
        <Col xs={12} lg={3}>
          <Select
            value={row.protocol || 'auto'}
            onChange={v => onChange({ ...row, protocol: v })}
            options={['auto', 'openai', 'claude', 'gemini'].map(p => ({ value: p, label: protocolLabel(p) }))}
            style={{ width: '100%' }}
          />
        </Col>
        {showTokenizer && (
          <Col xs={12} lg={4}>
            <Select
              value={row.tokenizer || 'auto'}
              onChange={v => onChange({ ...row, tokenizer: v })}
              options={tokenizers.map(t => ({ value: t.id, label: t.label }))}
              style={{ width: '100%' }}
            />
          </Col>
        )}
        <Col flex="auto" style={{ textAlign: 'right' }}>
          <Button danger type="text" icon={<DeleteOutlined />} onClick={onRemove} disabled={index === 0} />
        </Col>
      </Row>
    </div>
  )
}

export function TargetRowsEditor({ value = [], onChange = () => {}, tokenizers = [], showTokenizer = true }: {
  value?: TargetRow[]
  onChange?: (rows: TargetRow[]) => void
  tokenizers?: { id: string; label: string }[]
  showTokenizer?: boolean
}) {
  const { data: targets = [] } = useTargets()
  const rows = useMemo(() => (value && value.length ? value : [{ targetId: '', model: '', label: '', tokenizer: 'auto', protocol: 'auto' }]), [value])
  const update = (i: number, row: TargetRow) => {
    const next = rows.slice()
    next[i] = row
    onChange(next)
  }
  const remove = (i: number) => onChange(rows.filter((_, idx) => idx !== i))
  return (
    <div>
      {rows.map((row, i) => (
        <TargetRowItem
          key={i}
          row={row}
          index={i}
          targets={targets}
          tokenizers={tokenizers}
          showTokenizer={showTokenizer}
          onChange={r => update(i, r)}
          onRemove={() => remove(i)}
        />
      ))}
      <Button type="dashed" block icon={<PlusOutlined />} onClick={() => onChange([...rows, { targetId: '', model: '', label: '', tokenizer: 'auto', protocol: 'auto' }])}>
        添加对比目标
      </Button>
    </div>
  )
}
