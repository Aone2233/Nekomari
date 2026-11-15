import { createApp, h } from 'vue'
import { createPinia } from 'pinia'
import * as echarts from 'echarts/core'
import PingChart from '@/components/PingChart.vue'
import { useAppStore } from '@/stores/app'
import { getSharedRpc } from '@/utils/rpc'
import contract from './ping-role-contract.json'
import '@/styles/main.css'

const client = getSharedRpc().getClient()
const scenario = new URLSearchParams(location.search).get('scenario') ?? 'dual'
client.call = async (method: string, params: any) => {
  const hours = Number(params.hours ?? 1)
  const latest = Date.now()
  const spacing = hours >= 24 ? 3600_000 : 60_000
  const points = Array.from({ length: 10 }, (_, i) => new Date(latest - (9 - i) * spacing).toISOString())
  if (method === 'public:getPingMetricStats') {
    return { stats: contract.series.map((s) => ({ ...s, name: s.task_id === '28' ? contract.legacy_task.name : contract.pinned_task.name, type: s.task_id === '28' ? scenario === 'icmp' ? 'icmp' : scenario === 'unknown' ? undefined : 'dual' : 'tcp', interval: 60, loss: 0, avg: s.value, total: points.length })) }
  }
  if (method === 'public:queryMetrics') {
    const key = params.metric_keys[0]
    return { series: contract.series.map(({ value, ...tags }) => ({ metric_key: key, tags, interval_seconds: spacing / 1000, points: points.map(time => ({ time, value: key === 'ping.loss' ? 0 : value })) })) }
  }
  throw new Error(`Unexpected call: ${method}`)
}
const app = createApp({ render: () => h(PingChart, { uuid: 'fixture' }) })
app.use(createPinia())
useAppStore().publicSettings = {
  ping_record_preserve_time: 2160,
  theme_settings: { showPingReference: new URLSearchParams(location.search).get('reference') === '1' },
} as any
;(window as any).fixtureErrors = []
app.config.errorHandler = error => (window as any).fixtureErrors.push(String(error))
app.mount('#app')
;(window as any).chartSeries = () => {
  const el = document.querySelector('[_echarts_instance_]') as HTMLElement | null
  const chart = el && echarts.getInstanceByDom(el)
  return chart?.getOption().series
}
