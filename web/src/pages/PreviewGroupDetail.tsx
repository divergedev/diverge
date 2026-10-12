import { useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { stringify as yamlStringify } from 'yaml'
import { useGetPreviewGroup, useDeletePreviewGroup } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { TTLCountdown } from '@/components/TTLCountdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { TopologyView } from '@/components/topology/TopologyView'
import { isSafeExternalUrl } from '@/lib/utils'
import { ArrowLeft, Trash2, AlertCircle, Copy, ExternalLink } from 'lucide-react'

const conditionPhase = (status: string): string => {
  switch (status) {
    case 'True':
      return 'Ready'
    case 'False':
      return 'Error'
    case 'Unknown':
      return 'Pending'
    default:
      return 'Pending'
  }
}

export default function PreviewGroupDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading, error, refetch } = useGetPreviewGroup(namespace, name)
  const deletePg = useDeletePreviewGroup()
  const [activeTab, setActiveTab] = useState('overview')
  const [mutationError, setMutationError] = useState<string | null>(null)

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-16 space-y-4" role="alert">
        <AlertCircle className="h-10 w-10 text-destructive mx-auto" />
        <h2 className="text-xl font-semibold">Failed to load preview group</h2>
        <p className="text-muted-foreground">{error.message}</p>
        <Button variant="outline" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    )
  }

  const pg = data?.previewGroup
  if (!pg) {
    return (
      <div className="text-center py-16">
        <h2 className="text-xl font-semibold">Preview Group not found</h2>
      </div>
    )
  }

  const handleDelete = async () => {
    if (!confirm(`Delete preview group ${namespace}/${name}?`)) return
    setMutationError(null)
    try {
      await deletePg.mutateAsync({ namespace, name })
      navigate('/preview-groups')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to delete preview group')
    }
  }

  const rawExpiresAt = pg.status?.expiresAt as unknown
  const expiresAtIso =
    typeof rawExpiresAt === 'string'
      ? rawExpiresAt
      : (rawExpiresAt as { toDate?: () => Date } | undefined)?.toDate?.()?.toISOString?.()

  const routing = pg.spec?.routing
  const routingHeader =
    routing?.headerKey && routing?.headerValue
      ? `${routing.headerKey}: ${routing.headerValue}`
      : ''
  const safeExternalUrl = isSafeExternalUrl(routing?.externalUrl)
    ? routing?.externalUrl
    : undefined

  const services = pg.status?.services ?? []
  const serviceCount = pg.status?.serviceCount || services.length || 0
  const conditions = pg.status?.conditions ?? []

  return (
    <div className="space-y-6">
      {mutationError && (
        <div
          className="bg-destructive/10 border border-destructive/20 text-destructive px-4 py-3 rounded-md flex items-center gap-2 text-sm"
          role="alert"
        >
          <AlertCircle className="h-4 w-4 flex-shrink-0" />
          {mutationError}
        </div>
      )}

      <div className="flex items-center gap-4">
        <Link
          to="/preview-groups"
          aria-label="Back to preview groups"
          className="inline-flex items-center justify-center h-9 w-9 rounded-md text-sm font-medium hover:bg-accent hover:text-accent-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
        </Link>
        <div className="flex-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold">{pg.name}</h1>
            <StatusBadge phase={pg.status?.phase ?? ''} />
            {expiresAtIso && (
              <div className="flex items-center gap-1.5 text-sm">
                <span className="text-muted-foreground">TTL:</span>
                <TTLCountdown expiresAt={expiresAtIso} />
              </div>
            )}
          </div>
          <p className="text-muted-foreground">{pg.namespace}</p>
        </div>
        <div className="flex items-center gap-2">
          {safeExternalUrl && (
            <a href={safeExternalUrl} target="_blank" rel="noopener noreferrer">
              <Button variant="outline" size="sm">
                <ExternalLink className="h-4 w-4 mr-2" />
                Open Preview
              </Button>
            </a>
          )}
          <Button
            variant="destructive"
            size="sm"
            onClick={handleDelete}
            disabled={deletePg.isPending}
          >
            <Trash2 className="h-4 w-4 mr-2" />
            Delete
          </Button>
        </div>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="topology">Topology</TabsTrigger>
          <TabsTrigger value="conditions">Conditions</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Details &amp; Routing</CardTitle>
            </CardHeader>
            <CardContent className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Repository / Project</span>
                <span className="font-mono text-xs">{pg.spec?.source?.project || '—'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Branch</span>
                <span className="font-mono text-xs">{pg.spec?.source?.branch || '—'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">PR / MR</span>
                <span>{pg.spec?.source?.mr ? `#${pg.spec.source.mr}` : '—'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Routing Mode</span>
                <span>{routing?.mode || '—'}</span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">Routing Header</span>
                {routingHeader ? (
                  <div className="inline-flex items-center gap-1.5">
                    <code className="bg-muted px-1.5 py-0.5 rounded text-xs font-mono">
                      {routingHeader}
                    </code>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6"
                      aria-label="Copy routing header"
                      onClick={() => navigator.clipboard.writeText(routingHeader)}
                    >
                      <Copy className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                ) : (
                  <span>—</span>
                )}
              </div>
              <div className="flex justify-between items-center">
                <span className="text-muted-foreground">External URL</span>
                {safeExternalUrl ? (
                  <a
                    href={safeExternalUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-primary hover:underline text-xs font-mono"
                  >
                    {safeExternalUrl}
                    <ExternalLink className="h-3 w-3" />
                  </a>
                ) : (
                  <span>—</span>
                )}
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Service Count</span>
                <span>{serviceCount}</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">Services Status</CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {services.length > 0 ? (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Service</TableHead>
                      <TableHead>Phase</TableHead>
                      <TableHead>Environment</TableHead>
                      <TableHead>Changed Services</TableHead>
                      <TableHead>URL</TableHead>
                      <TableHead>Message</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {services.map((svc) => {
                      const targetNs = svc.namespace || namespace
                      const safeSvcUrl = isSafeExternalUrl(svc.url) ? svc.url : undefined
                      return (
                        <TableRow key={svc.name}>
                          <TableCell className="font-mono text-xs font-medium">
                            {svc.name}
                          </TableCell>
                          <TableCell>
                            <StatusBadge phase={svc.phase || 'Pending'} />
                          </TableCell>
                          <TableCell>
                            {svc.environmentName ? (
                              <Link
                                to={`/environments/${targetNs}/${svc.environmentName}`}
                                className="text-primary hover:underline font-mono text-xs"
                              >
                                {svc.environmentName}
                              </Link>
                            ) : (
                              <span className="text-muted-foreground text-xs">—</span>
                            )}
                          </TableCell>
                          <TableCell>
                            {svc.changedServices && svc.changedServices.length > 0 ? (
                              <div className="flex flex-wrap gap-1">
                                {svc.changedServices.map((cs) => (
                                  <Badge key={cs} variant="secondary" className="text-[10px] font-mono">
                                    {cs}
                                  </Badge>
                                ))}
                              </div>
                            ) : (
                              <span className="text-muted-foreground text-xs">—</span>
                            )}
                          </TableCell>
                          <TableCell>
                            {safeSvcUrl ? (
                              <a
                                href={safeSvcUrl}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex items-center gap-1 text-primary hover:underline text-xs font-mono"
                              >
                                {safeSvcUrl}
                                <ExternalLink className="h-3 w-3" />
                              </a>
                            ) : (
                              <span className="text-muted-foreground text-xs">—</span>
                            )}
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">
                            {svc.message || svc.reason || '—'}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              ) : (
                <p className="p-4 text-sm text-muted-foreground">No services reported</p>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="topology">
          <Card>
            <CardContent className="p-0">
              <TopologyView previewGroup={pg} />
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="conditions">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Conditions</CardTitle>
            </CardHeader>
            <CardContent>
              {conditions.length > 0 ? (
                <div className="space-y-3">
                  {conditions.map((c, idx) => (
                    <div
                      key={`${c.type}-${idx}`}
                      className="flex items-start justify-between gap-4 border-b border-border/50 pb-2 last:border-0 last:pb-0 text-sm"
                    >
                      <div className="space-y-0.5">
                        <div className="font-medium">{c.type}</div>
                        {c.reason && (
                          <div className="text-xs text-muted-foreground font-mono">{c.reason}</div>
                        )}
                        {c.message && (
                          <div className="text-xs text-muted-foreground">{c.message}</div>
                        )}
                      </div>
                      <StatusBadge phase={conditionPhase(c.status)} />
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">No conditions</p>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="yaml">
          <Card>
            <CardContent className="p-0">
              <pre className="text-xs p-4 overflow-auto max-h-[600px] font-mono">
                {yamlStringify(pg.toJson?.() ?? pg)}
              </pre>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
