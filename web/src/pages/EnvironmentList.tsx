import { useState } from 'react'
import { useListEnvironments } from '@/api/queries'
import { useWatchEnvironments } from '@/hooks/useWatch'
import { EnvironmentTable } from '@/components/EnvironmentTable'
import { CreateEnvironmentModal } from '@/components/CreateEnvironmentModal'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Plus, Search, Layers } from 'lucide-react'

export default function EnvironmentList() {
  const [namespace] = useState<string | undefined>(undefined)
  const [search, setSearch] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const { data, isLoading } = useListEnvironments({ namespace })
  useWatchEnvironments(namespace)

  const environments = data?.environments ?? []
  const filtered = search
    ? environments.filter((e) => e.name.toLowerCase().includes(search.toLowerCase()) || e.namespace.toLowerCase().includes(search.toLowerCase()))
    : environments

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Environments</h1>
          <p className="text-muted-foreground">Manage your preview environments</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4 mr-2" />Create Environment</Button>
      </div>

      <div className="flex items-center gap-4">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input placeholder="Search environments..." value={search} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
        </div>
      </div>

      {!isLoading && filtered.length === 0 ? (
        <EmptyState
          icon={Layers}
          title="No environments yet"
          description="Create your first preview environment to get started."
          action={<Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4 mr-2" />Create Environment</Button>}
        />
      ) : (
        <EnvironmentTable environments={filtered} isLoading={isLoading} />
      )}

      <CreateEnvironmentModal open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  )
}
