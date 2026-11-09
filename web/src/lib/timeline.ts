import type { TimelineEvent } from '@/api/types'
import { absTime } from './format'

export type Tone = 'critical' | 'resolved' | 'agent' | 'action' | 'user' | 'neutral' | 'warn'

export interface Described {
  icon: string
  tone: Tone
  text: string
  /** events with the same group key next to each other collapse into "×N" */
  group: string
  body?: string
}

const s = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

/** One-line description of a system timeline event. */
export function describeEvent(e: TimelineEvent): Described {
  const p = e.payload ?? {}
  const g = (k: string) => `${e.type}:${k}`
  switch (e.type) {
    case 'incident_opened':
      return { icon: 'incidents', tone: 'critical', text: 'Incident opened', group: g('') }
    case 'incident_reopened':
      return {
        icon: 'refresh',
        tone: 'critical',
        text: `Incident reopened (fired again within the reopen window)${
          p.analysis === 'reused' ? ' · previous analysis reused' : p.analysis === 'fix_failed' ? " · the fix didn't hold, investigating again" : p.analysis === 'new' ? ' · investigating' : ''
        }`,
        group: g(''),
      }
    case 'signal_firing':
      return { icon: 'bell', tone: 'critical', text: `Signal firing${p.name ? `: ${s(p.name)}` : ''}`, group: g(s(p.name)) }
    case 'signal_resolved':
      return { icon: 'check', tone: 'resolved', text: `Signal resolved${p.name ? `: ${s(p.name)}` : ''}`, group: g(s(p.name)) }
    case 'incident_status_changed':
      return {
        icon: p.to === 'resolved' ? 'check' : 'chevronRight',
        tone: p.to === 'resolved' || p.to === 'closed' ? 'resolved' : p.to === 'open' ? 'critical' : 'warn',
        text: `Status ${s(p.from) || '—'} → ${s(p.to)}${p.reason ? ` · ${s(p.reason)}` : ''}`,
        group: `${e.id}`,
      }
    case 'investigation_started':
      return { icon: 'sparkle', tone: 'agent', text: `Investigation started${p.trigger ? ` (${s(p.trigger)}${p.deep ? ', deep' : ''})` : ''}`, group: `${e.id}` }
    case 'investigation_failed':
      return { icon: 'info', tone: 'warn', text: 'AI investigation failed', body: s(p.reason) || s(p.error), group: `${e.id}` }
    case 'investigation_skipped':
      return { icon: 'info', tone: 'neutral', text: 'AI investigation skipped', body: s(p.reason), group: `${e.id}` }
    case 'mute_created':
      return { icon: 'mute', tone: 'neutral', text: `Muted${p.ends_at ? ` until ${absTime(s(p.ends_at), true)}` : ''}${p.reason ? ` · ${s(p.reason)}` : ''}`, group: `${e.id}` }
    case 'mute_ended':
      return { icon: 'bell', tone: 'neutral', text: 'Mute ended', group: g('') }
    case 'notification_routed': {
      const ch = Array.isArray(p.channels) ? p.channels.map(s).join(', ') : ''
      return { icon: 'send', tone: 'neutral', text: `Routed to ${ch || '—'}${p.reason ? ` · ${s(p.reason)}` : ''}`, group: g(ch) }
    }
    case 'notification_sent':
      return { icon: 'send', tone: 'neutral', text: `Notification sent${p.channel ? ` to ${s(p.channel)}` : ''}`, group: g(s(p.channel)) }
    case 'assigned':
      return { icon: 'user', tone: 'user', text: `Assigned to ${s(p.assignee) || s(p.name) || '—'}`, group: `${e.id}` }
    case 'incident_merged':
      return p.from_incident
        ? { icon: 'incidents', tone: 'agent', text: `Grouped here: ${s(p.alert) || `#${s(p.from_incident)}`} (#${s(p.from_incident)}), same problem`, body: s(p.reason), group: `${e.id}` }
        : { icon: 'incidents', tone: 'agent', text: `Same problem as #${s(p.into_incident)}: merged there`, body: s(p.reason), group: `${e.id}` }
    case 'storm_member':
      return { icon: 'incidents', tone: 'warn', text: `Grouped #${s(p.incident_id)}${p.title ? ` ${s(p.title)}` : ''}`, group: g('') }
    case 'action_approved':
    case 'action_rejected':
    case 'action_started':
    case 'action_succeeded':
    case 'action_failed':
    case 'action_expired':
      return { icon: 'bolt', tone: 'action', text: `${e.type.replace('action_', 'Action ')}: ${s(p.summary) || s(p.type)}`, group: `${e.id}` }
    default:
      return { icon: 'dot', tone: 'neutral', text: e.type.replace(/_/g, ' '), group: `${e.id}` }
  }
}

/** "Lucas via telegram", "agent", "system". */
export function originLabel(actorType: string, name: string, channel: string): string {
  if (actorType === 'agent') return 'agent'
  if (actorType === 'system') return 'system'
  const via = channel && channel !== 'system' ? channel : 'ui'
  return `${name || 'someone'} via ${via}`
}
