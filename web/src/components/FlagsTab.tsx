import { useState } from 'react'
import type { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/StatusBadge'
import {
  Flag,
  Copy,
  Check,
  ExternalLink,
  Server,
  Sliders,
  CheckCircle2,
  AlertCircle,
  Clock,
  Sparkles,
  Info,
} from 'lucide-react'

interface FlagsTabProps {
  environment: Environment
}

export function FlagsTab({ environment }: FlagsTabProps) {
  const [copiedKey, setCopiedKey] = useState<string | null>(null)

  const specFeatures = environment.spec?.features
  const provider = specFeatures?.provider || 'configmap'
  const rawOverrides = (specFeatures?.overrides ?? {}) as Record<string, string>
  const overrideEntries = Object.entries(rawOverrides)

  const status = environment.status
  const conditions = status?.conditions ?? []
  const featureCondition = conditions.find((c) => c.type === 'FeaturesReady')
  const featureEnvVars = (status?.featureEnvVars ?? {}) as Record<string, string>
  const envVarEntries = Object.entries(featureEnvVars)

  const handleCopy = (key: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedKey(key)
    setTimeout(() => setCopiedKey(null), 2000)
  }

  const getProviderInfo = () => {
    switch (provider.toLowerCase()) {
      case 'flipt':
        return {
          title: 'Flipt Feature Flags',
          desc: 'Remote feature flag engine with isolated ephemeral namespace.',
          badge: 'Flipt',
          color: 'bg-indigo-500/10 text-indigo-400 border-indigo-500/30',
          consoleUrl: featureEnvVars['FLIPT_URL']
            ? `${featureEnvVars['FLIPT_URL'].replace(/\/$/, '')}/namespaces/${featureEnvVars['FLIPT_NAMESPACE'] || `diverge-${environment.name}`}`
            : null,
        }
      case 'flagsmith':
        return {
          title: 'Flagsmith Enterprise',
          desc: 'Feature flags & remote config provider (preview stub).',
          badge: 'Flagsmith',
          color: 'bg-amber-500/10 text-amber-400 border-amber-500/30',
          consoleUrl: 'https://app.flagsmith.com',
        }
      case 'unleash':
        return {
          title: 'Unleash Enterprise',
          desc: 'Enterprise feature management with strategy evaluation (preview stub).',
          badge: 'Unleash',
          color: 'bg-cyan-500/10 text-cyan-400 border-cyan-500/30',
          consoleUrl: null,
        }
      case 'noop':
      case 'none':
        return {
          title: 'Disabled (No-op)',
          desc: 'Feature flag management is disabled for this environment.',
          badge: 'No-op',
          color: 'bg-muted text-muted-foreground border-border',
          consoleUrl: null,
        }
      case 'configmap':
      default:
        return {
          title: 'Kubernetes ConfigMap (flagd)',
          desc: 'In-cluster OpenFeature file provider using Kubernetes ConfigMaps.',
          badge: 'ConfigMap',
          color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30',
          consoleUrl: null,
        }
    }
  }

  const providerInfo = getProviderInfo()

  return (
    <div className="space-y-6">
      {/* Provider & Health Summary Cards */}
      <div className="grid gap-4 md:grid-cols-3">
        <Card className="md:col-span-2">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Sliders className="h-5 w-5 text-primary" />
                <CardTitle className="text-base font-semibold">{providerInfo.title}</CardTitle>
              </div>
              <Badge variant="outline" className={providerInfo.color}>
                {providerInfo.badge}
              </Badge>
            </div>
            <CardDescription className="text-xs">{providerInfo.desc}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-4 text-xs">
              <div>
                <span className="text-muted-foreground block">Active Overrides</span>
                <span className="font-semibold text-sm">{overrideEntries.length} configured</span>
              </div>
              <div>
                <span className="text-muted-foreground block">Target Namespace</span>
                <span className="font-mono text-xs truncate block" title={featureEnvVars['FLIPT_NAMESPACE'] || status?.featureConfigMap || environment.namespace}>
                  {featureEnvVars['FLIPT_NAMESPACE'] || status?.featureConfigMap || '—'}
                </span>
              </div>
              <div>
                <span className="text-muted-foreground block">Connection Secret</span>
                <span className="font-mono text-xs">{specFeatures?.connectionRef || 'Default'}</span>
              </div>
            </div>

            {providerInfo.consoleUrl && (
              <div className="pt-2">
                <a
                  href={providerInfo.consoleUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center text-xs text-primary hover:underline gap-1"
                >
                  <ExternalLink className="h-3.5 w-3.5" />
                  Open {providerInfo.badge} Console
                </a>
              </div>
            )}
          </CardContent>
        </Card>

        {/* Readiness Status Card */}
        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-base font-semibold">Readiness</CardTitle>
              {featureCondition ? (
                <StatusBadge
                  phase={
                    featureCondition.status === 'True'
                      ? 'Ready'
                      : featureCondition.status === 'False'
                        ? 'Error'
                        : 'Pending'
                  }
                />
              ) : (
                <Badge variant="outline">Unprovisioned</Badge>
              )}
            </div>
            <CardDescription className="text-xs">
              {featureCondition?.reason || 'FeaturesProvisioned'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-xs text-muted-foreground">
              {featureCondition?.message || 'Feature flags evaluated and ready for OpenFeature SDK consumers.'}
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Flag Overrides Table */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base font-semibold flex items-center gap-2">
                <Flag className="h-4 w-4 text-primary" />
                Feature Flag Overrides
              </CardTitle>
              <CardDescription className="text-xs">
                Dynamic evaluations and variant assignments scoped to this preview environment.
              </CardDescription>
            </div>
            <Badge variant="secondary" className="text-xs">
              {overrideEntries.length} {overrideEntries.length === 1 ? 'flag' : 'flags'}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {overrideEntries.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[30%]">Flag Key</TableHead>
                  <TableHead className="w-[15%]">Type</TableHead>
                  <TableHead className="w-[20%]">Override Value</TableHead>
                  <TableHead className="w-[25%]">Targeting Scope</TableHead>
                  <TableHead className="w-[10%] text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {overrideEntries.map(([key, value]) => {
                  const isBoolean = value === 'true' || value === 'false'
                  const boolValue = value === 'true'

                  return (
                    <TableRow key={key}>
                      <TableCell className="font-mono text-xs font-medium">
                        <div className="flex items-center gap-1.5">
                          <Flag className="h-3.5 w-3.5 text-muted-foreground" />
                          <span>{key}</span>
                        </div>
                      </TableCell>
                      <TableCell>
                        {isBoolean ? (
                          <Badge variant="outline" className="text-[10px] bg-blue-500/10 text-blue-400 border-blue-500/30">
                            Boolean
                          </Badge>
                        ) : (
                          <Badge variant="outline" className="text-[10px] bg-purple-500/10 text-purple-400 border-purple-500/30">
                            Variant
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell>
                        {isBoolean ? (
                          boolValue ? (
                            <Badge variant="default" className="bg-emerald-600 hover:bg-emerald-600 text-white font-mono text-xs">
                              true
                            </Badge>
                          ) : (
                            <Badge variant="secondary" className="font-mono text-xs">
                              false
                            </Badge>
                          )
                        ) : (
                          <Badge variant="outline" className="font-mono text-xs bg-purple-500/10 text-purple-300 border-purple-500/20">
                            {value}
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        <code className="bg-muted px-1.5 py-0.5 rounded text-[11px]">
                          diverge.environment == "{environment.name}"
                        </code>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          title={`Copy ${key}`}
                          onClick={() => handleCopy(key, key)}
                        >
                          {copiedKey === key ? (
                            <Check className="h-3.5 w-3.5 text-green-500" />
                          ) : (
                            <Copy className="h-3.5 w-3.5 text-muted-foreground" />
                          )}
                        </Button>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          ) : (
            <div className="py-12 px-4 text-center space-y-3">
              <Flag className="h-8 w-8 text-muted-foreground mx-auto stroke-1" />
              <div className="space-y-1">
                <p className="text-sm font-medium">No feature flag overrides configured</p>
                <p className="text-xs text-muted-foreground max-w-sm mx-auto">
                  Add flags in your environment spec or <code className="bg-muted px-1 rounded">diverge.yaml</code> to test experimental features without code changes.
                </p>
              </div>
              <pre className="text-left text-[11px] bg-muted p-3 rounded-md inline-block font-mono max-w-md mx-auto text-muted-foreground">
{`features:
  provider: ${provider}
  overrides:
    new_checkout: "true"
    tier_level: "beta-pro"`}
              </pre>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Injected Workload Environment Variables */}
      {envVarEntries.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base font-semibold flex items-center gap-2">
              <Server className="h-4 w-4 text-primary" />
              Injected Workload Environment Variables
            </CardTitle>
            <CardDescription className="text-xs">
              Automatically injected into preview pods for zero-config OpenFeature SDK initialization.
            </CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[35%]">Environment Variable</TableHead>
                  <TableHead className="w-[55%]">Injected Value</TableHead>
                  <TableHead className="w-[10%] text-right">Copy</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {envVarEntries.map(([envKey, envVal]) => (
                  <TableRow key={envKey}>
                    <TableCell className="font-mono text-xs font-semibold text-primary">
                      {envKey}
                    </TableCell>
                    <TableCell className="font-mono text-xs truncate max-w-xs" title={envVal}>
                      {envKey.includes('TOKEN') || envKey.includes('SECRET')
                        ? '••••••••••••••••'
                        : envVal}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        title={`Copy ${envKey}`}
                        onClick={() => handleCopy(envKey, envVal)}
                      >
                        {copiedKey === envKey ? (
                          <Check className="h-3.5 w-3.5 text-green-500" />
                        ) : (
                          <Copy className="h-3.5 w-3.5 text-muted-foreground" />
                        )}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
