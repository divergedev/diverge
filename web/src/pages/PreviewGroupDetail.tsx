import { useParams, Link, useNavigate } from 'react-router-dom'
import { useGetPreviewGroup, useDeletePreviewGroup } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ArrowLeft, Trash2 } from 'lucide-react'

export default function PreviewGroupDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading } = useGetPreviewGroup(namespace, name)
  const deletePg = useDeletePreviewGroup()

  if (isLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  const pg = data?.previewGroup
  if (!pg) {
    return <div className="text-center py-16"><h2 className="text-xl font-semibold">Preview Group not found</h2></div>
  }

  const handleDelete = async () => {
    if (!confirm(`Delete preview group ${namespace}/${name}?`)) return
    await deletePg.mutateAsync({ namespace, name })
    navigate('/preview-groups')
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <Link to="/preview-groups"><Button variant="ghost" size="icon"><ArrowLeft className="h-4 w-4" /></Button></Link>
        <div className="flex-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold">{pg.name}</h1>
            <StatusBadge phase={pg.status?.phase ?? ''} />
          </div>
          <p className="text-muted-foreground">{pg.namespace}</p>
        </div>
        <Button variant="destructive" size="sm" onClick={handleDelete}><Trash2 className="h-4 w-4 mr-2" />Delete</Button>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader><CardTitle className="text-base">Services</CardTitle></CardHeader>
          <CardContent>
            {pg.spec?.services?.length ? (
              <div className="space-y-3">
                {pg.spec.services.map((svc, i) => (
                  <div key={i} className="flex items-center justify-between p-2 rounded border">
                    <span className="font-medium text-sm">{svc.name}</span>
                    <StatusBadge phase={pg.status?.serviceStatuses?.[i]?.phase ?? 'Unknown'} />
                  </div>
                ))}
              </div>
            ) : <p className="text-sm text-muted-foreground">No services defined</p>}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Spec</CardTitle></CardHeader>
          <CardContent>
            <pre className="text-xs bg-muted p-3 rounded overflow-auto max-h-64">{JSON.stringify(pg.spec?.toJson?.() ?? pg.spec, null, 2)}</pre>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
