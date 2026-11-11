import type { Live } from './types'

export interface StressPlan {
  mode: 'fixed' | 'duration' | 'ramp'
  plannedRequests: number
  plannedMs: number
  percent: number
  remainingMs: number
  estimated: boolean
}

/**
 * Derive progress for a stress run. Fixed mode is request-based; duration and
 * ramp modes are time-based because their request count depends on latency.
 */
export function stressPlan(config: any, live?: Live): StressPlan | null {
  if (!config || !live) return null
  const load = config.load || {}
  const modelCount = Array.isArray(config.models) && config.models.length ? config.models.length : 1
  const parallel = !!config.parallelModels
  const elapsed = Math.max(0, Number(live.elapsedMs || 0))
  const mode: StressPlan['mode'] = load.mode === 'duration' || load.mode === 'ramp' ? load.mode : 'fixed'

  let plannedRequests = 0
  let plannedMs = 0
  let percent = 0
  let remainingMs = 0
  let estimated = false

  if (mode === 'fixed') {
    plannedRequests = modelCount * Number(load.totalRequests || 0) || Number(live.planned || 0)
    const completed = Number(live.completed || 0)
    percent = plannedRequests > 0 ? Math.min(100, (completed / plannedRequests) * 100) : 0
    if (completed > 0 && elapsed > 0) {
      remainingMs = Math.max(0, (plannedRequests - completed) * (elapsed / completed))
      estimated = true
    }
  } else if (mode === 'duration') {
    const sec = Number(load.durationSec || 0)
    plannedMs = sec * 1000 * (parallel ? 1 : modelCount)
    percent = plannedMs > 0 ? Math.min(100, (elapsed / plannedMs) * 100) : 0
    remainingMs = Math.max(0, plannedMs - elapsed)
  } else {
    const start = Number(load.rampStart || 1)
    const end = Number(load.rampEnd || start)
    const step = Math.max(1, Number(load.rampStep || 1))
    const stepSec = Number(load.rampStepSec || 0)
    const levels = Math.max(1, Math.floor((end - start) / step) + 1)
    plannedMs = levels * stepSec * 1000 * (parallel ? 1 : modelCount)
    percent = plannedMs > 0 ? Math.min(100, (elapsed / plannedMs) * 100) : 0
    remainingMs = Math.max(0, plannedMs - elapsed)
  }

  return { mode, plannedRequests, plannedMs, percent, remainingMs, estimated }
}
