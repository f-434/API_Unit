import type {
  Attack, DiffResponse, Item, ItemDetail, Kind, Live, Meta, Paged, Price, Prompt,
  Run, RunRow, RunView, Snapshot, SnapshotDetail, Target, TokenizerStatus,
} from './types'

export class ApiError extends Error {
  status: number
  payload: any
  constructor(status: number, payload: any) {
    super(typeof payload === 'string' ? payload : payload?.error || `HTTP ${status}`)
    this.status = status
    this.payload = payload
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers || {}),
    },
  })
  const text = await res.text()
  let payload: any = null
  if (text) {
    try { payload = JSON.parse(text) } catch { payload = text }
  }
  if (!res.ok) {
    if (res.status === 401 && !location.pathname.startsWith('/login')) {
      location.href = '/login'
    }
    throw new ApiError(res.status, payload)
  }
  return payload as T
}

const get = <T>(path: string) => request<T>(path)
const post = <T>(path: string, body?: any) => request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
const put = <T>(path: string, body?: any) => request<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) })
const del = <T>(path: string) => request<T>(path, { method: 'DELETE' })

export const api = {
  health: () => get<{ ok: boolean; version: string }>('/api/health'),
  authStatus: () => get<{ authEnabled: boolean; authenticated: boolean }>('/api/auth/status'),
  login: (password: string) => post<{ ok: boolean }>('/api/auth/login', { password }),
  logout: () => post<{ ok: boolean }>('/api/auth/logout'),
  meta: () => get<Meta>('/api/meta'),
  settings: () => get<any>('/api/settings'),

  targets: () => get<{ items: Target[] }>('/api/targets'),
  createTarget: (body: any) => post<{ item: Target }>('/api/targets', body),
  updateTarget: (id: string, body: any) => put<{ item: Target }>(`/api/targets/${id}`, body),
  deleteTarget: (id: string) => del<{ ok: boolean }>(`/api/targets/${id}`),
  testTarget: (body: { baseUrl: string; apiKey?: string; protocol?: string }) => post<any>('/api/targets/test', body),
  targetModels: (id: string) => post<any>(`/api/targets/${id}/models`),

  prompts: () => get<{ items: Prompt[] }>('/api/prompts'),
  createPrompt: (body: any) => post<{ item: Prompt }>('/api/prompts', body),
  updatePrompt: (id: string, body: any) => put<{ item: Prompt }>(`/api/prompts/${id}`, body),
  deletePrompt: (id: string) => del<{ ok: boolean }>(`/api/prompts/${id}`),
  importPrompts: (prompts: any[]) => post<{ created: number }>('/api/prompts/import', { prompts }),

  attacks: () => get<{ items: Attack[]; categories: { id: string; label: string }[] }>('/api/attacks'),
  createAttack: (body: any) => post<{ item: Attack }>('/api/attacks', body),
  updateAttack: (id: string, body: any) => put<{ item: Attack }>(`/api/attacks/${id}`, body),
  deleteAttack: (id: string) => del<{ ok: boolean }>(`/api/attacks/${id}`),
  importAttacks: (attacks: any[]) => post<{ created: number }>('/api/attacks/import', { attacks }),
  resetAttacks: () => post<{ ok: boolean; count: number }>('/api/attacks/reset-builtin'),

  prices: () => get<{ items: Price[] }>('/api/prices'),
  createPrice: (body: any) => post<{ item: Price }>('/api/prices', body),
  updatePrice: (id: string, body: any) => put<{ item: Price }>(`/api/prices/${id}`, body),
  deletePrice: (id: string) => del<{ ok: boolean }>(`/api/prices/${id}`),

  tokenizerStatus: () => get<TokenizerStatus>('/api/tokenizers/status'),
  tokenizerCount: (body: any) => post<any>('/api/tokenizers/count', body),
  ensureGeminiTokenizer: () => post<any>('/api/tokenizers/ensure'),
  reloadTokenizers: () => post<any>('/api/tokenizers/reload'),
  resetGeminiTokenizer: (download = true) => post<any>('/api/tokenizers/reset', { download }),
  purgeRuns: (days: number) => post<{ deleted: number }>('/api/settings/purge', { days }),

  createRun: (kind: Kind, config: any) => post<{ id: string; run: Run }>('/api/runs', { kind, config }),
  runs: (params: { kind?: string; limit?: number; offset?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.kind) q.set('kind', params.kind)
    q.set('limit', String(params.limit ?? 50))
    q.set('offset', String(params.offset ?? 0))
    return get<{ items: RunRow[]; total: number }>(`/api/runs?${q}`)
  },
  run: (id: string) => get<RunView>(`/api/runs/${id}`),
  stopRun: (id: string) => post<{ ok: boolean }>(`/api/runs/${id}/stop`),
  deleteRun: (id: string) => del<{ ok: boolean }>(`/api/runs/${id}`),
  items: (id: string, params: Record<string, string | number | boolean | undefined>) => {
    const q = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== '' && v !== false) q.set(k, String(v))
    })
    return get<{ items: Item[]; total: number; limit: number; offset: number }>(`/api/runs/${id}/items?${q}`)
  },
  item: (id: string, itemId: number) => get<{ item: ItemDetail }>(`/api/runs/${id}/items/${itemId}`),

  snapshots: (params: Record<string, string | number | undefined> = {}) => {
    const q = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== '') q.set(k, String(v))
    })
    return get<Paged<Snapshot>>(`/api/snapshots?${q}`)
  },
  runSnapshots: (runId: string, params: Record<string, string | number | undefined> = {}) => {
    const q = new URLSearchParams()
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== '') q.set(k, String(v))
    })
    return get<Paged<Snapshot>>(`/api/runs/${runId}/snapshots?${q}`)
  },
  snapshot: (id: number) => get<{ item: SnapshotDetail }>(`/api/snapshots/${id}`),
  diff: (leftId: number, rightId: number, mode: 'wire' | 'canonical') =>
    post<DiffResponse>('/api/diffs', { leftId, rightId, mode }),

  exportRunUrl: (id: string, format: 'json' | 'html' | 'csv', items = false) =>
    `/api/runs/${id}/export?format=${format}${items ? '&items=1' : ''}`,
  exportDiffUrl: (left: number, right: number, mode: 'wire' | 'canonical', format: 'json' | 'html') =>
    `/api/diffs/export?left=${left}&right=${right}&mode=${mode}&format=${format}`,
}

export function download(url: string) {
  const a = document.createElement('a')
  a.href = url
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

export interface RunSubscription {
  close: () => void
}

export function subscribeRun(
  id: string,
  handlers: {
    onLive?: (live: Live) => void
    onDone?: (done: { runId: string; status: string; error?: string }) => void
    onError?: (err: Event) => void
  },
): RunSubscription {
  const es = new EventSource(`/api/runs/${id}/events`)
  const parse = (e: MessageEvent) => {
    try { return JSON.parse(e.data) } catch { return null }
  }
  es.addEventListener('snapshot', (e) => {
    const data = parse(e as MessageEvent)
    if (data) handlers.onLive?.(data as Live)
  })
  es.addEventListener('update', (e) => {
    const data = parse(e as MessageEvent)
    if (data) handlers.onLive?.(data as Live)
  })
  es.addEventListener('done', (e) => {
    const data = parse(e as MessageEvent)
    if (data) handlers.onDone?.(data)
    es.close()
  })
  es.onerror = (e) => {
    handlers.onError?.(e)
    if (es.readyState === EventSource.CLOSED) es.close()
  }
  return { close: () => es.close() }
}
