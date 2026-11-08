import type { IncidentFilters } from '@/api/types'

/** Supported `key:value` tokens in the search bar. Values may be comma separated for list keys. */
export const SEARCH_KEYS = {
  status: { list: true, values: ['open', 'acknowledged', 'resolved', 'closed'] },
  severity: { list: true, values: ['critical', 'warning', 'info', 'none'] },
  verdict: { list: true, values: ['actionable', 'noise', 'unknown'] },
  cluster: { list: false, values: [] as string[] },
  namespace: { list: false, values: [] as string[] },
  muted: { list: false, values: ['true', 'false'] },
} as const

export type SearchKey = keyof typeof SEARCH_KEYS

export interface Token {
  key?: SearchKey
  value: string
  raw: string
}

const aliases: Record<string, SearchKey> = {
  sev: 'severity',
  st: 'status',
  ns: 'namespace',
}

function splitTokens(text: string): string[] {
  const out: string[] = []
  const re = /(\S+:"[^"]*"|"[^"]*"|\S+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(text))) out.push(m[1])
  return out
}

export function tokenize(text: string): Token[] {
  return splitTokens(text).map((raw) => {
    const i = raw.indexOf(':')
    if (i > 0) {
      const k = raw.slice(0, i).toLowerCase()
      const key = (aliases[k] ?? k) as SearchKey
      if (key in SEARCH_KEYS) {
        return { key, value: raw.slice(i + 1).replace(/^"|"$/g, ''), raw }
      }
    }
    return { value: raw.replace(/^"|"$/g, ''), raw }
  })
}

export function parseSearch(text: string): IncidentFilters {
  const f: IncidentFilters = {}
  const free: string[] = []
  for (const t of tokenize(text)) {
    if (!t.key) {
      free.push(t.value)
      continue
    }
    if (!t.value) continue
    switch (t.key) {
      case 'status':
      case 'severity':
      case 'verdict': {
        const vals = t.value.split(',').map((v) => v.trim().toLowerCase()).filter(Boolean)
        f[t.key] = [...new Set([...(f[t.key] ?? []), ...vals])]
        break
      }
      case 'cluster':
      case 'namespace':
        f[t.key] = t.value
        break
      case 'muted':
        f.muted = t.value === 'true' || t.value === 'yes' || t.value === '1'
        break
    }
  }
  if (free.length) f.q = free.join(' ')
  return f
}

/** Toggle a value of a list key in the search text (used by quick filter chips). */
export function toggleToken(text: string, key: SearchKey, value: string): string {
  const tokens = tokenize(text)
  const existing = tokens.find((t) => t.key === key)
  if (!existing) return [text.trim(), `${key}:${value}`].filter(Boolean).join(' ')
  const vals = existing.value.split(',').filter(Boolean)
  const next = vals.includes(value) ? vals.filter((v) => v !== value) : [...vals, value]
  const rest = tokens.filter((t) => t !== existing).map((t) => t.raw)
  if (next.length) rest.push(`${key}:${next.join(',')}`)
  return rest.join(' ')
}

export function setToken(text: string, key: SearchKey, value: string | null): string {
  const rest = tokenize(text)
    .filter((t) => t.key !== key)
    .map((t) => t.raw)
  if (value) rest.push(`${key}:${value.includes(' ') ? `"${value}"` : value}`)
  return rest.join(' ')
}

export function hasTokenValue(text: string, key: SearchKey, value: string): boolean {
  return tokenize(text).some((t) => t.key === key && t.value.split(',').includes(value))
}

// ---- time ranges ----

export const RANGE_PRESETS = [
  { id: '1h', label: 'Last 1 hour', ms: 3600e3 },
  { id: '6h', label: 'Last 6 hours', ms: 6 * 3600e3 },
  { id: '24h', label: 'Last 24 hours', ms: 24 * 3600e3 },
  { id: '7d', label: 'Last 7 days', ms: 7 * 86400e3 },
  { id: '30d', label: 'Last 30 days', ms: 30 * 86400e3 },
] as const

export interface TimeRange {
  preset?: string
  since?: string
  until?: string
}

export function resolveRange(r: TimeRange, now: number): { since: string; until?: string } {
  const p = RANGE_PRESETS.find((x) => x.id === r.preset)
  if (p) {
    // round to the minute so query keys stay stable between ticks
    const t = Math.floor(now / 60000) * 60000
    return { since: new Date(t - p.ms).toISOString() }
  }
  return { since: r.since ?? new Date(now - 86400e3).toISOString(), until: r.until }
}

export function rangeLabel(r: TimeRange): string {
  const p = RANGE_PRESETS.find((x) => x.id === r.preset)
  if (p) return p.label
  const f = (s?: string) =>
    s ? new Date(s).toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : 'now'
  return `${f(r.since)} → ${f(r.until)}`
}
