import { useParams, useNavigate, Link } from 'react-router-dom'
import { useGetEnvironment, useDeleteEnvironment, useExtendTTL } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { TTLCountdown } from '@/components/TTLCountdown'
import { LogViewer } from '@/components/LogViewer'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { ArrowLeft, ExternalLink, Copy, Trash2, Clock, AlertCircle } from 'lucide-react'
import { useState } from 'react'
import { stringify as yamlStringify } from 'yaml'

export default function EnvironmentDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading, error, refetch } = useGetEnvironment(namespace, name)
  const deleteEnv = useDeleteEnvironment()
  const extendTtl = useExtendTTL()
  const [mutationError, setMutationError] = useState<string | null>(null)

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
      await extendTtl.mutateAsync({ namespace, name, duration: '3600s' })
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to extend TTL')
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
          <Button variant="outline" size="sm" onClick={handleExtendTTL} disabled={extendTtl.isPending}>
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

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
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
    </div>
  )
}
