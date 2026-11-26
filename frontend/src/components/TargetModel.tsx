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
        size="middle"
        value={value}
        onChange={onChange}
        options={models.map((m: any) => ({ value: m.id, label: m.id }))}
        placeholder={targetId ? placeholder : '请先选择上游'}
        disabled={!targetId}
        style={{ width: '100%', minHeight: 32 }}
        filterOption={(input, option) => String(option?.value || '').toLowerCase().includes(input.toLowerCase())}
      />
      <Tooltip title={isError ? `获取模型失败：${(error as any)?.message || ''}` : '刷新模型列表'}>
        <Button
          size="middle"
          style={{ height: 32 }}
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
          size="middle"
          maxTagCount="responsive"
          value={value}
          onChange={onChange}
          tokenSeparators={[',', '\n', ' ']}
          options={models.map((m: any) => ({ value: m.id, label: `${m.id}${m.ownedBy ? ` · ${m.ownedBy}` : ''}` }))}
          placeholder={targetId ? '选择或输入模型，可多选' : '请先选择上游'}
          disabled={!targetId}
          style={{ width: '100%', minHeight: 32 }}
        />
        <Button size="middle" style={{ height: 32 }} icon={<ReloadOutlined />} loading={isLoading} disabled={!targetId} onClick={() => refetch()} />
      </Space.Compact>
      {isError && <Typography.Text type="danger" style={{ fontSize: 12 }}>模型列表获取失败：{(error as any)?.message}</Typography.Text>}
    </Space>
  )
}

export interface TargetRow {
  targetId: string
  /** 单模型模式使用。 */
  model: string
  /** 多模型模式使用；单模型模式忽略。 */
  models?: string[]
  label?: string
  tokenizer?: string
  protocol?: string
}

// 根据渠道的 Token 口径推导本地分词器；未标注或无法识别时回退自动。
export function tokenizerForCaliber(tokenFamily?: string): string {
  const root = (tokenFamily || '').split('/')[0].trim().toLowerCase()
  if (['gemini', 'google'].includes(root)) return 'gemini'
  if (['gpt', 'openai', 'azure', 'az'].includes(root)) return 'tiktoken'
  return 'auto'
}

function TargetRowItem({ row, index, targets, tokenizers, showTokenizer, autoFromTarget, multiModel, onChange, onRemove }: {
  row: TargetRow
  index: number
  targets: Target[]
  autoFromTarget: boolean
  tokenizers: { id: string; label: string }[]
  showTokenizer: boolean
  multiModel: boolean
  onChange: (row: TargetRow) => void
  onRemove: () => void
}) {
  const setTarget = (targetId: string) => {
    const t = targets.find(x => x.id === targetId)
    const changed = row.targetId !== targetId
    onChange({
      ...row,
      targetId,
      label: row.label || (t ? `${t.name}` : ''),
      model: changed ? '' : row.model,
      models: changed ? [] : (row.models || []),
      // 审计场景下协议与分词器由渠道配置自动映射，同时保留在表单值里供提交使用。
      ...(autoFromTarget && t ? {
        protocol: t.protocol || 'auto',
        tokenizer: tokenizerForCaliber(t.tokenFamily),
      } : {}),
    })
  }
  return (
    <div className="case-card">
      <Row gutter={[10, 10]} align="middle">
        <Col xs={24} lg={autoFromTarget ? 9 : 6}>
          <TargetSelect value={row.targetId} onChange={setTarget} targets={targets} placeholder={`目标 ${index + 1}`} />
        </Col>
        <Col xs={24} lg={autoFromTarget ? 9 : 6}>
          {multiModel ? (
            <ModelMultiSelect
              targetId={row.targetId}
              value={row.models || []}
              onChange={v => onChange({ ...row, models: v || [] })}
            />
          ) : (
            <ModelAutoComplete targetId={row.targetId} value={row.model} onChange={v => onChange({ ...row, model: v })} />
          )}
        </Col>
        <Col xs={12} lg={autoFromTarget ? 4 : 3}>
          <Input value={row.label} onChange={e => onChange({ ...row, label: e.target.value })} placeholder="对比标签" />
        </Col>
        {!autoFromTarget && (
          <Col xs={12} lg={3}>
            <Select
              value={row.protocol || 'auto'}
              onChange={v => onChange({ ...row, protocol: v })}
              options={['auto', 'openai', 'claude', 'gemini'].map(p => ({ value: p, label: protocolLabel(p) }))}
              style={{ width: '100%' }}
            />
          </Col>
        )}
        {!autoFromTarget && showTokenizer && (
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

export function TargetRowsEditor({ value = [], onChange = () => {}, tokenizers = [], showTokenizer = true, autoFromTarget = false, multiModel = false }: {
  value?: TargetRow[]
  onChange?: (rows: TargetRow[]) => void
  tokenizers?: { id: string; label: string }[]
  showTokenizer?: boolean
  /** 协议与分词器由所选的渠道自动映射，不再手选。 */
  autoFromTarget?: boolean
  /** 同一上游可选择多个模型；提交时由页面展开为多个目标。 */
  multiModel?: boolean
}) {
  const { data: targets = [] } = useTargets()
  const newRow = (): TargetRow => ({
    targetId: '', model: '', models: multiModel ? [] : undefined,
    label: '', tokenizer: 'auto', protocol: 'auto',
  })
  const rows = useMemo(() => (value && value.length ? value : [newRow()]), [value, multiModel])
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
          autoFromTarget={autoFromTarget}
          multiModel={multiModel}
          onChange={r => update(i, r)}
          onRemove={() => remove(i)}
        />
      ))}
      <Button type="dashed" block icon={<PlusOutlined />} onClick={() => onChange([...rows, newRow()])}>
        添加对比目标
      </Button>
    </div>
  )
}
