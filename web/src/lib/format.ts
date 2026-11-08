const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto', style: 'short' })

export function relTime(ts?: string | null, now = Date.now()): string {
  if (!ts) return '—'
  const t = new Date(ts).getTime()
  if (Number.isNaN(t)) return '—'
  const s = Math.round((t - now) / 1000)
  const a = Math.abs(s)
  if (a < 45) return s <= 0 ? 'just now' : 'in a few s'
  if (a < 3600) return rtf.format(Math.round(s / 60), 'minute')
  if (a < 86400) return rtf.format(Math.round(s / 3600), 'hour')
  if (a < 86400 * 30) return rtf.format(Math.round(s / 86400), 'day')
  return absTime(ts, true)
}

/** Compact age like "4m", "3h", "2d". */
export function age(ts?: string | null, now = Date.now()): string {
  if (!ts) return '—'
  const s = Math.max(0, Math.round((now - new Date(ts).getTime()) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

export function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const s = Math.round(seconds)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60 ? `${s % 60}s` : ''}`.trim()
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h ${m % 60 ? `${m % 60}m` : ''}`.trim()
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24 ? `${h % 24}h` : ''}`.trim()
}

const dtf = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: 'short',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
})
const dtfShort = new Intl.DateTimeFormat(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' })

export function absTime(ts?: string | null, short = false): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return '—'
  return (short ? dtfShort : dtf).format(d)
}

export function clockTime(ts: string): string {
  return new Date(ts).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function countdown(ts: string, now = Date.now()): string {
  const s = Math.round((new Date(ts).getTime() - now) / 1000)
  if (s <= 0) return 'expired'
  if (s < 60) return `${s}s left`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s left`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m left`
}

export function sevKey(sev: string): 'critical' | 'warning' | 'info' | 'other' {
  const s = (sev || '').toLowerCase()
  if (s === 'critical' || s === 'page' || s === 'error') return 'critical'
  if (s === 'warning' || s === 'warn') return 'warning'
  if (s === 'info') return 'info'
  return 'other'
}

export function pct(n: number): string {
  return `${Math.round(n * 100)}%`
}
