import { useGetClusterInfo, useGetCurrentUser, useListPermissions } from '@/api/queries'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Server, User, Shield, Activity } from 'lucide-react'

export default function ClusterInfo() {
  const { data: cluster, isLoading: clusterLoading } = useGetClusterInfo()
  const { data: user } = useGetCurrentUser()
  const { data: perms } = useListPermissions()

  if (clusterLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">Cluster</h1>
        <p className="text-muted-foreground">Cluster information and access control</p>
      </div>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Version</CardTitle>
            <Server className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent><div className="text-2xl font-bold">{cluster?.version ?? '—'}</div></CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Environments</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent><div className="text-2xl font-bold">{cluster?.environmentCount ?? 0}</div></CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Preview Groups</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent><div className="text-2xl font-bold">{cluster?.previewGroupCount ?? 0}</div></CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Health</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent><div className="text-2xl font-bold text-green-400">{cluster?.healthy ? 'Healthy' : 'Unhealthy'}</div></CardContent>
        </Card>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2"><User className="h-4 w-4" /><CardTitle className="text-base">Current User</CardTitle></div>
          </CardHeader>
          <CardContent className="space-y-2">
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Username</span><span>{user?.username ?? '—'}</span></div>
            <div className="flex justify-between text-sm"><span className="text-muted-foreground">Groups</span><span>{user?.groups?.join(', ') ?? '—'}</span></div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2"><Shield className="h-4 w-4" /><CardTitle className="text-base">Permissions</CardTitle></div>
          </CardHeader>
          <CardContent>
            {perms?.permissions?.length ? (
              <div className="space-y-1">
                {perms.permissions.map((p: { resource: string; verbs?: string[] }, i: number) => (
                  <div key={i} className="text-sm flex justify-between">
                    <span>{p.resource}</span>
                    <span className="text-muted-foreground">{p.verbs?.join(', ')}</span>
                  </div>
                ))}
              </div>
            ) : <p className="text-sm text-muted-foreground">No permissions data</p>}
          </CardContent>
        </Card>
      </div>

      {cluster?.namespaces?.length ? (
        <Card>
          <CardHeader><CardTitle className="text-base">Namespaces</CardTitle></CardHeader>
          <CardContent>
            <div className="flex flex-wrap gap-2">
              {cluster.namespaces.map((ns: string) => (
                <span key={ns} className="px-2 py-1 rounded bg-muted text-sm">{ns}</span>
              ))}
            </div>
          </CardContent>
        </Card>
      ) : null}
    </div>
  )
}
