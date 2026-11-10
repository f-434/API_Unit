import ReactECharts from 'echarts-for-react'
import type { Live, SeriesPoint } from '../types'

const axisStyle = {
  axisLine: { lineStyle: { color: '#cbd5e1' } },
  axisLabel: { color: '#64748b', fontSize: 11 },
  splitLine: { lineStyle: { color: '#eef2f7' } },
}

export function ChartCard({ title, option, height = 220, extra }: { title: string; option: any; height?: number; extra?: React.ReactNode }) {
  return (
    <div className="panel" style={{ padding: 14 }}>
      <div className="panel-title" style={{ marginBottom: 4 }}>{title}{extra}</div>
      <ReactECharts option={option} style={{ height }} notMerge lazyUpdate opts={{ renderer: 'canvas' }} />
    </div>
  )
}

function xAxis(series: SeriesPoint[]) {
  return series.map(s => `${s.sec}s`)
}

export function LiveCharts({ live }: { live: Live }) {
  return <SeriesCharts series={live.series || []} />
}

export function SeriesCharts({ series }: { series: SeriesPoint[] }) {
  const x = xAxis(series)
  const baseGrid = { left: 44, right: 16, top: 24, bottom: 24 }
  const tooltip = { trigger: 'axis' }
  const legend = { top: 0, textStyle: { fontSize: 11, color: '#64748b' } }

  const throughput = {
    animation: false, grid: baseGrid, tooltip, legend,
    xAxis: { type: 'category', data: x, ...axisStyle },
    yAxis: { type: 'value', ...axisStyle },
    series: [
      { name: '成功', type: 'bar', stack: 'total', data: series.map(s => s.success), itemStyle: { color: '#16a34a' }, barMaxWidth: 14 },
      { name: '失败', type: 'bar', stack: 'total', data: series.map(s => s.failed), itemStyle: { color: '#dc2626' }, barMaxWidth: 14 },
      { name: '发起', type: 'line', smooth: true, symbol: 'none', data: series.map(s => s.started), lineStyle: { color: '#2563eb', width: 1.5 } },
    ],
  }
  const latency = {
    animation: false, grid: baseGrid, tooltip,
    xAxis: { type: 'category', data: x, ...axisStyle },
    yAxis: { type: 'value', name: 'ms', nameTextStyle: { color: '#94a3b8', fontSize: 10 }, ...axisStyle },
    series: [
      { name: '平均延迟', type: 'line', smooth: true, symbol: 'none', data: series.map(s => Number(s.avgLatencyMs.toFixed(1))), lineStyle: { color: '#7c3aed', width: 2 }, areaStyle: { opacity: 0.08 } },
    ],
  }
  const concurrency = {
    animation: false, grid: baseGrid, tooltip,
    xAxis: { type: 'category', data: x, ...axisStyle },
    yAxis: { type: 'value', ...axisStyle },
    series: [
      { name: '实际并发', type: 'line', smooth: true, symbol: 'none', data: series.map(s => Number(s.activeConcurrency.toFixed(2))), lineStyle: { color: '#0ea5e9', width: 2 }, areaStyle: { opacity: 0.16 } },
    ],
  }
  const rps = {
    animation: false, grid: baseGrid, tooltip,
    xAxis: { type: 'category', data: x, ...axisStyle },
    yAxis: { type: 'value', ...axisStyle },
    series: [
      { name: '成功/秒', type: 'bar', data: series.map(s => Number(s.rps.toFixed(2))), itemStyle: { color: '#0ea5e9' }, barMaxWidth: 14 },
    ],
  }
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: 14 }}>
      <ChartCard title="吞吐（每秒完成）" option={throughput} />
      <ChartCard title="平均延迟" option={latency} />
      <ChartCard title="实际并发" option={concurrency} />
      <ChartCard title="成功率 RPS" option={rps} />
    </div>
  )
}

export function BarChart({ title, categories, series, horizontal = false, height = 240 }: {
  title: string
  categories: string[]
  series: { name: string; data: number[]; color?: string }[]
  horizontal?: boolean
  height?: number
}) {
  const option = {
    animation: false,
    tooltip: { trigger: 'axis' },
    legend: { top: 0, textStyle: { fontSize: 11, color: '#64748b' } },
    grid: { left: horizontal ? 110 : 44, right: 18, top: 28, bottom: 24 },
    xAxis: horizontal
      ? { type: 'value', ...axisStyle }
      : { type: 'category', data: categories, ...axisStyle },
    yAxis: horizontal
      ? { type: 'category', data: categories, ...axisStyle }
      : { type: 'value', ...axisStyle },
    series: series.map(s => ({
      name: s.name,
      type: 'bar',
      data: s.data,
      itemStyle: { color: s.color },
      barMaxWidth: 20,
    })),
  }
  return <ChartCard title={title} option={option} height={height} />
}

export function DonutChart({ title, data, height = 240 }: {
  title: string
  data: { name: string; value: number; color?: string }[]
  height?: number
}) {
  const option = {
    animation: false,
    tooltip: { trigger: 'item' },
    legend: { bottom: 0, textStyle: { fontSize: 11, color: '#64748b' } },
    series: [{
      type: 'pie',
      radius: ['46%', '70%'],
      center: ['50%', '44%'],
      avoidLabelOverlap: true,
      label: { show: false },
      data: data.map(d => ({ ...d, itemStyle: d.color ? { color: d.color } : undefined })),
    }],
  }
  return <ChartCard title={title} option={option} height={height} />
}
