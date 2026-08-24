import { useParams, Link, useNavigate } from 'react-router-dom'
import { useGetPreviewGroup, useDeletePreviewGroup } from '@/api/queries'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { TopologyView } from '@/components/topology/TopologyView'
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

      {/* Service Topology */}
      <Card>
        <CardHeader><CardTitle className="text-base">Service Topology</CardTitle></CardHeader>
        <CardContent className="p-0">
          <TopologyView previewGroup={pg} />
        </CardContent>
      </Card>

      {/* Spec */}
      <Card>
        <CardHeader><CardTitle className="text-base">Spec</CardTitle></CardHeader>
        <CardContent>
          <pre className="text-xs bg-muted p-3 rounded overflow-auto max-h-64">{JSON.stringify(pg.spec?.toJson?.() ?? pg.spec, null, 2)}</pre>
        </CardContent>
      </Card>
    </div>
  )
}

