export type MainTab = 'overview' | 'details'
export type Period = 'today' | '24h' | '7d' | '30d' | '60d'
export type TableView = 'model' | 'account' | 'apiKey' | 'endpoint'
export type ViewMode = 'costs' | 'tokens'

export interface UsageItem {
  requests?: number
  promptTokens?: number
  completionTokens?: number
  cachedTokens?: number
  cost?: number
  lastUsed?: string
  rawModel?: string
  accountName?: string
  keyName?: string
  endpoint?: string
  provider?: string
  key?: string
}

export interface RecentRequestItem {
  status?: string
  model?: string
  provider?: string
  promptTokens?: number
  cachedTokens?: number
  completionTokens?: number
  timestamp?: string
}
export interface ActiveRequestItem {
  model?: string
  provider?: string
  account?: string
  count?: number
}


export interface RequestDetailItem {
  id?: string
  status?: string
  timestamp?: string
  provider?: string
  model?: string
  latency?: {
    total?: number
    ttft?: number
  }
  tokens?: {
    prompt_tokens?: number
    completion_tokens?: number
    cached_tokens?: number
    cache_read_input_tokens?: number
  }
  [key: string]: unknown
}

export interface StatsData {
  totalRequests?: number
  totalPromptTokens?: number
  totalCompletionTokens?: number
  totalCachedTokens?: number
  totalCost?: number
  byProvider?: Record<string, UsageItem>
  byModel?: Record<string, UsageItem>
  byAccount?: Record<string, UsageItem>
  byApiKey?: Record<string, UsageItem>
  byEndpoint?: Record<string, UsageItem>
  activeRequests?: ActiveRequestItem[]
  recentRequests?: RecentRequestItem[]
  errorProvider?: string
  pending?: unknown
}

export const PERIODS: { value: Period; label: string }[] = [
  { value: 'today', label: 'Today' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7D' },
  { value: '30d', label: '30D' },
  { value: '60d', label: '60D' },
]

export const TABLE_OPTIONS: { value: TableView; label: string }[] = [
  { value: 'model', label: 'Usage by Model' },
  { value: 'account', label: 'Usage by Account' },
  { value: 'apiKey', label: 'Usage by API Key' },
  { value: 'endpoint', label: 'Usage by Endpoint' },
]

export function fmt(n?: number): string {
  return (n || 0).toLocaleString()
}

export function cachedTokensFor(detail: RequestDetailItem): number {
  return detail.tokens?.cached_tokens ?? detail.tokens?.cache_read_input_tokens ?? 0
}

export function fmtCost(n?: number): string {
  return '$' + (n || 0).toFixed(2)
}

export function timeAgo(timestamp?: string): string {
  if (!timestamp) return 'just now'
  const diff = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000)
  if (diff < 60) return `${Math.max(1, diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}
