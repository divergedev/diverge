// Topology graph data model — decoupled from proto types for testability
// and future extensibility (e.g., OTel trace-derived edges)

export interface TopologyGraph {
  ingress: IngressNodeData | null
  services: ServiceNodeData[]
  dependencies: DependencyNodeData[]
  connections: TopologyConnection[]
}

// --- Node Data Types ---

export interface IngressNodeData {
  id: string
  routingMode: 'header' | 'subdomain' | 'cookie' | 'namespace'
  externalUrl: string
  headerKey: string
  headerValue: string
  baseDomain: string
  provider: string
  hasCookie: boolean
}

export interface ServiceNodeData {
  id: string
  name: string
  namespace: string
  mode: 'image' | 'local' | 'baseline'
  image: string
  port: number
  protocol: 'http' | 'grpc'
  pathPrefix: string
  phase: string
  reason: string
  message: string
  lastLogSnippet: string
  environmentName: string // child Environment CR for drill-down
  url: string
  isChanged: boolean // highlighted if in deploy.changedServices
  traceExplorerUrl?: string // deep-link to external trace viewer (Jaeger/Tempo)
  isInstrumented?: boolean  // whether OTel auto-instrumentation is active
}

export interface DependencyNodeData {
  id: string
  kind: 'database' | 'temporal' | 'kafka'
  label: string // human-readable: "PostgreSQL", "Temporal Task Queue", "Kafka Topic"
  detail: string // mode for DB, target queue/topic name for async
  isShared: boolean // ⚠️ warning if database.mode === 'shared'
  status: string // databaseStatus or empty
  parentServiceId: string // which service this belongs to
}

// --- Connection Types ---

export type ConnectionStatus = 'active' | 'deploying' | 'error' | 'inactive'

export interface TraceEdgeData {
  requestRate: number    // req/s
  errorRate: number      // 0-1 percentage
  p99Latency: number     // milliseconds
}

export interface TopologyConnection {
  id: string
  from: string // node id
  to: string // node id
  fromPort: 'right' // always right side of source
  toPort: 'left' // always left side of target
  status: ConnectionStatus
  label?: string // optional path prefix or protocol label
  traceData?: TraceEdgeData  // populated when trace metrics available
}

// --- Mode metadata for tooltips ---

export const MODE_LABELS: Record<string, { label: string; description: string }> = {
  image: { label: 'Deployed', description: 'Running as a pod in the cluster' },
  local: { label: 'Local', description: 'Proxied from your machine (e.g. Tailscale)' },
  baseline: { label: 'Baseline', description: 'Using the shared cluster service' },
}

export const DB_MODE_LABELS: Record<string, { label: string; warning: boolean }> = {
  shared: { label: 'Shared (Production)', warning: true },
  schema: { label: 'Schema Isolated', warning: false },
  snapshot: { label: 'Snapshot Copy', warning: false },
  fresh: { label: 'Fresh Database', warning: false },
}
