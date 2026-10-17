import { useState } from 'react'
import { useListEnvironments, useGetClusterInfo } from '@/api/queries'
import { useWatchEnvironments } from '@/hooks/useWatch'
import { EnvironmentTable } from '@/components/EnvironmentTable'
import { CreateEnvironmentModal } from '@/components/CreateEnvironmentModal'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Plus, Search, Layers, DollarSign, Moon } from 'lucide-react'
import { estimateEnvironmentHourlyCostUSD, formatHourlyCostUSD, isEnvironmentHibernated } from '@/lib/finops'

export default function EnvironmentList() {
  const [namespace] = useState<string | undefined>(undefined)
  const [search, setSearch] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const { data, isLoading } = useListEnvironments({ namespace })
  const { data: clusterInfo } = useGetClusterInfo()
  useWatchEnvironments(namespace)

  const environments = data?.environments ?? []
  const filtered = search
    ? environments.filter((e) => e.name.toLowerCase().includes(search.toLowerCase()) || e.namespace.toLowerCase().includes(search.toLowerCase()))
    : environments

  const totalHourlyCost = environments.reduce((sum, e) => sum + estimateEnvironmentHourlyCostUSD(e), 0)
  const monthlyRunRate = totalHourlyCost * 730
  const hibernatedCount = environments.filter(isEnvironmentHibernated).length
  const activeCount = environments.length - hibernatedCount

  const quota = clusterInfo?.namespaceQuota
  const hasCostQuota = (quota?.maxHourlyCostUsd ?? 0) > 0
  const hasActiveQuota = (quota?.maxActiveEnvironments ?? 0) > 0
  const hasCnpgQuota = (quota?.maxCnpgClusters ?? 0) > 0
  const hasAnyQuota = hasCostQuota || hasActiveQuota || hasCnpgQuota
  const quotaPolicy = quota?.quotaExceedPolicy || 'hibernate-oldest'
  const cnpgCount = environments.filter((e) => {
    const mode = (e.spec?.database?.mode || e.status?.databaseProvider || '').toLowerCase()
    return mode === 'cnpg' || mode === 'fresh' || mode === 'snapshot'
  }).length

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Environments</h1>
          <p className="text-muted-foreground">Manage your preview environments</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4 mr-2" />Create Environment</Button>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Estimated Hourly Spend</CardTitle>
            <DollarSign className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold font-mono">{formatHourlyCostUSD(totalHourlyCost)}</div>
            <p className="text-xs text-muted-foreground mt-1">
              ~${monthlyRunRate.toFixed(2)}/mo projected run-rate
            </p>
            {hasCostQuota && (
              <p className="text-xs font-mono text-muted-foreground mt-1">
                ${totalHourlyCost.toFixed(3)} / ${quota!.maxHourlyCostUsd.toFixed(3)}/hr budget
              </p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Compute State</CardTitle>
            <Moon className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {activeCount} Active · {hibernatedCount} Hibernated
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              Scale-to-zero idle hibernation
            </p>
            {hasAnyQuota && (
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground mt-1">
                {hasActiveQuota && (
                  <span className="font-mono">{activeCount} / {quota!.maxActiveEnvironments} max active</span>
                )}
                {hasCnpgQuota && (
                  <span className="font-mono">{cnpgCount} / {quota!.maxCnpgClusters} CNPG</span>
                )}
                <span className="inline-flex items-center rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">
                  {quotaPolicy}
                </span>
              </div>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Environments</CardTitle>
            <Layers className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{environments.length}</div>
          </CardContent>
        </Card>
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
