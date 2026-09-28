import { useParams, useNavigate, Link } from 'react-router-dom'
import { useGetEnvironment, useDeleteEnvironment, useExtendTTL, useListHookJobs } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { TTLCountdown } from '@/components/TTLCountdown'
import { LogViewer } from '@/components/LogViewer'
import { HooksTab } from '@/components/HooksTab'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Select } from '@/components/ui/select'
import { useState, useEffect } from 'react'
import { stringify as yamlStringify } from 'yaml'
import { TopologyView } from '@/components/topology/TopologyView'
import { FlagsTab } from '@/components/FlagsTab'
import { ArrowLeft, ExternalLink, Copy, Trash2, Clock, AlertCircle, Flag, CheckCircle } from 'lucide-react'

const TTL_DURATIONS = [
  { label: '1 hour', value: '3600s' },
  { label: '2 hours', value: '7200s' },
  { label: '4 hours', value: '14400s' },
  { label: '8 hours', value: '28800s' },
  { label: '24 hours', value: '86400s' },
  { label: '72 hours', value: '259200s' },
] as const

export default function EnvironmentDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading, error, refetch } = useGetEnvironment(namespace, name)
  const deleteEnv = useDeleteEnvironment()
  const extendTtl = useExtendTTL()
  const hookJobs = useListHookJobs(namespace, name)
  const failedHookCount = (hookJobs.data?.jobs ?? []).filter(j => j.phase === 'Failed').length
  const [mutationError, setMutationError] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState('overview')
  const [extendDialogOpen, setExtendDialogOpen] = useState(false)
  const [selectedDuration, setSelectedDuration] = useState('3600s')
  const [successMessage, setSuccessMessage] = useState<string | null>(null)

  useEffect(() => {
    if (!successMessage) return
    const timer = setTimeout(() => setSuccessMessage(null), 4000)
    return () => clearTimeout(timer)
  }, [successMessage])

  if (isLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  if (error) {
    return (
      <div className="text-center py-16 space-y-4" role="alert">
        <AlertCircle className="h-10 w-10 text-destructive mx-auto" />
        <h2 className="text-xl font-semibold">Failed to load environment</h2>
        <p className="text-muted-foreground">{error.message}</p>
        <Button variant="outline" onClick={() => refetch()}>Retry</Button>
      </div>
    )
  }

  const env = data?.environment
  if (!env) {
    return <div className="text-center py-16"><h2 className="text-xl font-semibold">Environment not found</h2></div>
  }

  const featureFlagCount = Object.keys(env.spec?.features?.overrides ?? {}).length

  const handleDelete = async () => {
    if (!confirm(`Delete environment ${namespace}/${name}?`)) return
    setMutationError(null)
    try {
      await deleteEnv.mutateAsync({ namespace, name })
      navigate('/')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to delete environment')
    }
  }

  const handleExtendTTL = async () => {
    setMutationError(null)
    try {
      await extendTtl.mutateAsync({ namespace, name, duration: selectedDuration })
      setExtendDialogOpen(false)
      const label = TTL_DURATIONS.find(d => d.value === selectedDuration)?.label ?? selectedDuration
      setSuccessMessage(`TTL extended by ${label}`)
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to extend TTL')
      setExtendDialogOpen(false)
    }
  }

  const conditionPhase = (status: string): string => {
    switch (status) {
      case 'True': return 'Ready'
      case 'False': return 'Error'
      case 'Unknown': return 'Pending'
      default: return 'Pending'
    }
  }

  return (
    <div className="space-y-6">
      {mutationError && (
        <div className="bg-destructive/10 border border-destructive/20 text-destructive px-4 py-3 rounded-md flex items-center gap-2 text-sm" role="alert">
          <AlertCircle className="h-4 w-4 flex-shrink-0" />
          {mutationError}
        </div>
      )}

      {successMessage && (
        <div className="bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-emerald-400 px-4 py-3 rounded-md flex items-center gap-2 text-sm animate-in fade-in slide-in-from-top-2" role="status">
          <CheckCircle className="h-4 w-4 flex-shrink-0" />
          {successMessage}
        </div>
      )}

      <div className="flex items-center gap-4">
        <Link to="/" aria-label="Back to environments" className="inline-flex items-center justify-center h-9 w-9 rounded-md text-sm font-medium hover:bg-accent hover:text-accent-foreground"><ArrowLeft className="h-4 w-4" /></Link>
        <div className="flex-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold">{env.name}</h1>
            <StatusBadge phase={env.status?.phase ?? ''} />
          </div>
          <p className="text-muted-foreground">{env.namespace}</p>
        </div>
        <div className="flex items-center gap-2">
          {env.status?.url && (
            <>
              <a href={env.status.url} target="_blank" rel="noreferrer">
                <Button variant="outline" size="sm"><ExternalLink className="h-4 w-4 mr-2" />Open Preview</Button>
              </a>
              <Button variant="ghost" size="icon" aria-label="Copy preview URL" onClick={() => navigator.clipboard.writeText(env.status?.url ?? '')}>
                <Copy className="h-4 w-4" />
              </Button>
            </>
          )}
          <Button variant="outline" size="sm" onClick={() => setExtendDialogOpen(true)}>
            <Clock className="h-4 w-4 mr-2" />Extend TTL
          </Button>
          <Button variant="destructive" size="sm" onClick={handleDelete} disabled={deleteEnv.isPending}>
            <Trash2 className="h-4 w-4 mr-2" />Delete
          </Button>
        </div>
      </div>

      <div className="flex items-center gap-4 text-sm">
        <span className="text-muted-foreground">Branch:</span>
        <span>{env.spec?.source?.branch ?? '—'}</span>
        <span className="text-muted-foreground">TTL:</span>
        <TTLCountdown expiresAt={env.status?.expiresAt?.toDate?.()?.toISOString?.()} />
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="topology">Topology</TabsTrigger>
          <TabsTrigger value="features">
            Features
            {featureFlagCount > 0 && (
              <span className="ml-1.5 inline-flex items-center justify-center h-5 min-w-5 px-1 rounded-full bg-primary/10 text-primary text-xs font-medium">
                {featureFlagCount}
              </span>
            )}
          </TabsTrigger>
          <TabsTrigger value="hooks">
            Hooks
            {failedHookCount > 0 && (
              <span className="ml-1.5 inline-flex items-center justify-center h-5 min-w-5 px-1 rounded-full bg-destructive text-destructive-foreground text-xs font-medium">
                {failedHookCount}
              </span>
            )}
          </TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>

        <TabsContent value="topology">
          <Card>
            <CardContent className="p-0">
              <TopologyView environment={env} />
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="features">
          <FlagsTab environment={env} />
        </TabsContent>

        <TabsContent value="overview">
          {failedHookCount > 0 && (
            <div className="bg-destructive/10 border border-destructive/20 text-destructive px-4 py-3 rounded-md flex items-center gap-2 text-sm mb-4" role="alert">
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
              <span>{failedHookCount} hook{failedHookCount > 1 ? 's' : ''} failed.</span>
              <button
                className="underline hover:no-underline font-medium"
                onClick={() => setActiveTab('hooks')}
              >
                View Hooks tab →
              </button>
            </div>
          )}
          <div className="grid gap-4 md:grid-cols-2">
            <Card>
              <CardHeader><CardTitle className="text-base">Spec</CardTitle></CardHeader>
              <CardContent>
                <pre className="text-xs bg-muted p-3 rounded overflow-auto max-h-64">{JSON.stringify(env.spec?.toJson?.() ?? env.spec, null, 2)}</pre>
              </CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle className="text-base">Conditions</CardTitle></CardHeader>
              <CardContent>
                {env.status?.conditions?.length ? (
                  <div className="space-y-2">
                    {env.status.conditions.map((c: { type: string; status: string }, i: number) => (
                      <div key={i} className="flex items-center justify-between text-sm">
                        <span>{c.type}</span>
                        <StatusBadge phase={conditionPhase(c.status)} />
                      </div>
                    ))}
                  </div>
                ) : <p className="text-sm text-muted-foreground">No conditions</p>}
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="hooks">
          <HooksTab namespace={namespace} environmentName={name} />
        </TabsContent>

        <TabsContent value="logs">
          <LogViewer namespace={namespace} environmentName={name} className="h-[600px]" />
        </TabsContent>

        <TabsContent value="yaml">
          <Card>
            <CardContent className="p-0">
              <pre className="text-xs p-4 overflow-auto max-h-[600px] font-mono">
                {yamlStringify(env.toJson?.() ?? env, { indent: 2 })}
              </pre>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Dialog open={extendDialogOpen} onClose={() => setExtendDialogOpen(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Extend TTL</DialogTitle>
            <DialogDescription>Choose how long to extend the environment&apos;s time-to-live.</DialogDescription>
          </DialogHeader>
          <Select
            value={selectedDuration}
            onChange={e => setSelectedDuration(e.target.value)}
            aria-label="Extension duration"
          >
            {TTL_DURATIONS.map(d => (
              <option key={d.value} value={d.value}>{d.label}</option>
            ))}
          </Select>
          <DialogFooter>
            <Button variant="outline" onClick={() => setExtendDialogOpen(false)}>Cancel</Button>
            <Button onClick={handleExtendTTL} disabled={extendTtl.isPending}>
              {extendTtl.isPending ? 'Extending…' : 'Extend'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
