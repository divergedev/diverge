import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import type { PartialMessage } from '@bufbuild/protobuf'
import type { PreviewGroup, PreviewGroupServiceSpec } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { previewGroupClient } from '@/api/client'
import { queryKeys, useCreatePreviewGroup, useListAllEnvironments, useUpdatePreviewGroup } from '@/api/queries'
import {
  COMPOSED_GROUP_LABEL,
  DEFAULT_ROUTING_HEADER_KEY,
  GROUP_MEMBERS_ANNOTATION,
  appendPreviewEnvParam,
  extractEnvironmentCandidate,
  formatGroupMemberBadge,
  isAbortedConflictError,
  isStaleEnvironmentRevision,
  normalizeGroupSlug,
  parseGroupMembers,
  previewGroupPath,
  sameGroupMemberOwner,
  sameReferencedEnvironment,
  serializeGroupMembers,
  type GroupMemberRecord,
} from '@/lib/previewgroup'
import { Plus, Trash2 } from 'lucide-react'

const TTL_OPTIONS = [
  { label: '1 hour', value: '3600' },
  { label: '4 hours', value: '14400' },
  { label: '8 hours', value: '28800' },
  { label: '24 hours', value: '86400' },
  { label: '48 hours', value: '172800' },
  { label: 'No limit', value: '' },
]

const DB_MODE_OPTIONS = [
  { label: "Per-service (keep each MR's own DB config)", value: '' },
  { label: 'Shared Neon CoW branch (neon)', value: 'neon' },
  { label: 'Shared PostgreSQL schema (schema)', value: 'schema' },
  { label: 'Shared CloudNativePG cluster (cnpg)', value: 'cnpg' },
  { label: 'Shared fresh instance (fresh)', value: 'fresh' },
  { label: 'Shared snapshot clone (snapshot)', value: 'snapshot' },
  { label: 'Shared baseline DB (shared)', value: 'shared' },
]

const DNS_1123_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/
const DNS_SUBDOMAIN_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

interface CustomServiceRow {
  id: string
  name: string
  image: string
  namespace: string
  mode: 'image' | 'baseline'
}

export interface ComposePreviewGroupModalProps {
  open: boolean
  onClose: () => void
  existingGroup?: PreviewGroup
}

export function ComposePreviewGroupModal({ open, onClose, existingGroup }: ComposePreviewGroupModalProps) {
  const isJoinMode = Boolean(existingGroup)
  const formIdPrefix = useId()
  const navigate = useNavigate()

  const queryClient = useQueryClient()
  const {
    data: envData,
    isLoading: envsLoading,
    error: envsError,
    refetch: refetchEnvs,
  } = useListAllEnvironments({ enabled: open })
  const createGroup = useCreatePreviewGroup()
  const updateGroup = useUpdatePreviewGroup()
  const sessionRef = useRef(0)
  const submittingRef = useRef(false)
  const prevOpenRef = useRef(open)

  const [isSubmitting, setIsSubmitting] = useState(false)

  const [name, setName] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  const [nameEditedManually, setNameEditedManually] = useState(false)
  const [namespace, setNamespace] = useState('default')
  const [headerKey, setHeaderKey] = useState(DEFAULT_ROUTING_HEADER_KEY)
  const [headerValue, setHeaderValue] = useState('')
  const [externalUrl, setExternalUrl] = useState('')
  const [ttlSeconds, setTtlSeconds] = useState('86400')
  const [sharedDbMode, setSharedDbMode] = useState('')
  const [targetCluster, setTargetCluster] = useState('')
  const [clusterSecret, setClusterSecret] = useState('')
  const [selectedEnvKeys, setSelectedEnvKeys] = useState<string[]>([])
  const [customServices, setCustomServices] = useState<CustomServiceRow[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    sessionRef.current += 1
    if (!open) {
      submittingRef.current = false
      setIsSubmitting(false)
      if (prevOpenRef.current) {
        void queryClient.cancelQueries({ queryKey: queryKeys.allEnvironments() })
      }
    }
    if (open && !prevOpenRef.current) {
      submittingRef.current = false
      setIsSubmitting(false)
      setName('')
      setNameTouched(false)
      setNameEditedManually(false)
      setNamespace('default')
      setHeaderKey(DEFAULT_ROUTING_HEADER_KEY)
      setHeaderValue('')
      setExternalUrl('')
      setTtlSeconds('86400')
      setSharedDbMode('')
      setTargetCluster('')
      setClusterSecret('')
      setSelectedEnvKeys([])
      setCustomServices([])
      setError('')
    }
    prevOpenRef.current = open
    return () => {
      sessionRef.current += 1
      submittingRef.current = false
      void queryClient.cancelQueries({ queryKey: queryKeys.allEnvironments() })
    }
  }, [open, queryClient])

  const handleDismiss = () => {
    sessionRef.current += 1
    submittingRef.current = false
    setIsSubmitting(false)
    void queryClient.cancelQueries({ queryKey: queryKeys.allEnvironments() })
    onClose()
  }

  const candidates = useMemo(() => {
    const envs = envData?.environments ?? []
    return envs
      .filter((e) => e.labels?.['divergedev.com/managed-by'] !== 'diverge-previewgroup')
      .map(extractEnvironmentCandidate)
      .filter((c): c is NonNullable<typeof c> => c !== null)
  }, [envData?.environments])

  const toggleCandidate = (key: string, branchSlug: string) => {
    setError('')
    const isAdding = !selectedEnvKeys.includes(key)
    setSelectedEnvKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
    if (!isJoinMode && isAdding && !nameEditedManually && !name && branchSlug) {
      const normalized = normalizeGroupSlug(branchSlug)
      if (normalized) {
        setName(normalized)
      }
    }
  }

  const addCustomRow = () => {
    setError('')
    setCustomServices((prev) => [
      ...prev,
      {
        id: `${Date.now()}-${prev.length}`,
        name: '',
        image: '',
        namespace: namespace || 'default',
        mode: 'image',
      },
    ])
  }

  const updateCustomRow = (id: string, patch: Partial<CustomServiceRow>) => {
    setError('')
    setCustomServices((prev) => prev.map((row) => (row.id === id ? { ...row, ...patch } : row)))
  }

  const removeCustomRow = (id: string) => {
    setError('')
    setCustomServices((prev) => prev.filter((row) => row.id !== id))
  }

  const validateGroupNameField = (val: string): string => {
    const trimmed = val.trim()
    if (!trimmed) return 'Group name is required'
    if (trimmed.length > 63 || !DNS_1123_RE.test(trimmed)) {
      return 'Name must be lowercase alphanumeric with hyphens (1-63 chars)'
    }
    return ''
  }

  const handleNameBlur = () => {
    if (!nameTouched) return
    const msg = validateGroupNameField(name)
    if (msg) setError(msg)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (submittingRef.current) return
    setError('')

    const effectiveName = isJoinMode ? (existingGroup?.name ?? '') : name.trim()
    if (!isJoinMode) {
      const nameErr = validateGroupNameField(effectiveName)
      if (nameErr) {
        setNameTouched(true)
        setError(nameErr)
        return
      }
    }

    const nsClean = namespace.trim() || 'default'
    if (nsClean.length > 63 || !DNS_1123_RE.test(nsClean)) {
      setError('Namespace must be a valid lowercase DNS label')
      return
    }

    const trimmedCluster = targetCluster.trim()
    const trimmedSecret = clusterSecret.trim()
    if (trimmedCluster && (trimmedCluster.length > 63 || !DNS_1123_RE.test(trimmedCluster))) {
      setError('Target cluster must be lowercase alphanumeric with hyphens')
      return
    }
    if (trimmedSecret && (trimmedSecret.length > 253 || !DNS_SUBDOMAIN_RE.test(trimmedSecret))) {
      setError('Spoke kubeconfig secret must be a valid Kubernetes DNS subdomain name')
      return
    }
    if (trimmedCluster && trimmedCluster !== 'local' && !trimmedSecret) {
      setError('Spoke kubeconfig secret is required when targeting a remote cluster')
      return
    }
    if (trimmedSecret && trimmedCluster === 'local') {
      setError('Target cluster "local" cannot specify a spoke kubeconfig secret')
      return
    }

    const candidatesByKey = new Map(candidates.map((c) => [c.key, c]))
    const missingEnvKeys = selectedEnvKeys.filter((k) => !candidatesByKey.has(k))
    if (missingEnvKeys.length > 0) {
      setError(
        `Selected environment ${missingEnvKeys.join(', ')} is no longer available. Uncheck it or refresh your selection.`,
      )
      return
    }
    const chosenCandidates = selectedEnvKeys.map((k) => candidatesByKey.get(k)!)
    const incomingSpecs: PartialMessage<PreviewGroupServiceSpec>[] = []
    const incomingMembers = new Map<string, GroupMemberRecord>()
    const seenIncomingNames = new Set<string>()

    for (const c of chosenCandidates) {
      if (seenIncomingNames.has(c.serviceName)) {
        setError(`Duplicate service "${c.serviceName}" selected across multiple environments`)
        return
      }
      seenIncomingNames.add(c.serviceName)
      incomingSpecs.push({
        ...c.spec,
        database: sharedDbMode ? undefined : c.spec.database,
      })
      incomingMembers.set(c.serviceName, c.member)
    }

    for (const row of customServices) {
      const svcName = row.name.trim()
      const svcImg = row.image.trim()
      const svcNs = row.namespace.trim() || nsClean
      if (!svcName) {
        setError('Custom service name cannot be empty')
        return
      }
      if (!DNS_1123_RE.test(svcName)) {
        setError(`Service name "${svcName}" must be lowercase alphanumeric with hyphens`)
        return
      }
      if (seenIncomingNames.has(svcName)) {
        setError(`Duplicate service "${svcName}" specified`)
        return
      }
      if (row.mode === 'image' && !svcImg) {
        setError(`Service "${svcName}" requires a container image when mode is image`)
        return
      }
      seenIncomingNames.add(svcName)
      incomingSpecs.push({
        name: svcName,
        mode: row.mode,
        image: row.mode === 'image' ? svcImg : undefined,
        namespace: svcNs,
      })
      if (row.mode !== 'baseline') {
        incomingMembers.set(svcName, {
          service: svcName,
          namespace: svcNs,
          provider: 'local',
          updatedAt: new Date().toISOString(),
        })
      }
    }

    if (incomingSpecs.length === 0) {
      setError('Select at least one open PR/MR environment or add a custom service')
      return
    }

    const submitSession = sessionRef.current
    const isCurrentSession = () => open && sessionRef.current === submitSession

    submittingRef.current = true
    setIsSubmitting(true)
    try {
      if (isJoinMode && existingGroup) {
        let currentGroup: PreviewGroup = existingGroup
        for (let attempt = 0; attempt < 3; attempt++) {
          const existingMembers = parseGroupMembers(currentGroup.annotations)
          const byService = new Map<string, GroupMemberRecord>()
          for (const m of existingMembers) byService.set(m.service, m)

          const effectiveSharedDbMode = sharedDbMode || currentGroup.spec?.database?.mode?.trim() || ''
          const priorServices: PartialMessage<PreviewGroupServiceSpec>[] = (currentGroup.spec?.services ?? []).map(
            (s) => ({
              ...s,
              database: effectiveSharedDbMode ? undefined : s.database,
            }),
          )

          for (const inc of incomingSpecs) {
            const normalizedInc: PartialMessage<PreviewGroupServiceSpec> = {
              ...inc,
              database: effectiveSharedDbMode ? undefined : inc.database,
            }
            const idx = priorServices.findIndex((s) => s.name === normalizedInc.name)
            if (idx >= 0) {
              const prevOwner = byService.get(normalizedInc.name ?? '')
              const nextOwner = incomingMembers.get(normalizedInc.name ?? '')
              if (priorServices[idx].mode !== 'baseline') {
                const prevIsOwned = Boolean(prevOwner && ((prevOwner.mr ?? 0) > 0 || prevOwner.environmentName))
                if (normalizedInc.mode === 'baseline' && prevIsOwned) {
                  const ownerLabel = formatGroupMemberBadge(prevOwner) || prevOwner?.environmentName || 'another MR'
                  throw new Error(
                    `Service "${normalizedInc.name}" is owned by ${ownerLabel} in group "${currentGroup.name}". Detach it first before reverting it to baseline.`,
                  )
                }
                if (normalizedInc.mode !== 'baseline' && !sameGroupMemberOwner(prevOwner, nextOwner)) {
                  const ownerLabel = formatGroupMemberBadge(prevOwner) || prevOwner?.environmentName || 'another MR'
                  throw new Error(
                    `Service "${normalizedInc.name}" is already owned by ${ownerLabel} in group "${currentGroup.name}". Detach it first before replacing it.`,
                  )
                }
                if (
                  normalizedInc.mode !== 'baseline' &&
                  sameReferencedEnvironment(prevOwner, nextOwner) &&
                  isStaleEnvironmentRevision(
                    prevOwner?.environmentResourceVersion,
                    nextOwner?.environmentResourceVersion,
                  )
                ) {
                  throw new Error(
                    `Selected revision for "${normalizedInc.name}" (${nextOwner?.environmentResourceVersion}) is older than the group's active revision (${prevOwner?.environmentResourceVersion}). Refresh environments and try again.`,
                  )
                }
              }
              priorServices[idx] = {
                ...normalizedInc,
                clusterRef: normalizedInc.clusterRef ?? priorServices[idx].clusterRef,
                postDeploy: normalizedInc.postDeploy ?? priorServices[idx].postDeploy,
              }
            } else {
              priorServices.push(normalizedInc)
            }
          }

          for (const [svc, m] of incomingMembers) byService.set(svc, m)

          const orderedMembers: GroupMemberRecord[] = []
          for (const s of priorServices) {
            if (s.mode === 'baseline' || !s.name) continue
            const rec = byService.get(s.name)
            if (rec) orderedMembers.push(rec)
          }

          const nextAnnotations: Record<string, string> = { ...(currentGroup.annotations ?? {}) }
          if (orderedMembers.length > 0) {
            nextAnnotations[GROUP_MEMBERS_ANNOTATION] = serializeGroupMembers(orderedMembers)
          } else {
            delete nextAnnotations[GROUP_MEMBERS_ANNOTATION]
          }

          try {
            await updateGroup.mutateAsync({
              previewGroup: {
                name: currentGroup.name,
                namespace: currentGroup.namespace,
                resourceVersion: currentGroup.resourceVersion,
                labels: {
                  ...(currentGroup.labels ?? {}),
                  [COMPOSED_GROUP_LABEL]: 'true',
                },
                annotations: nextAnnotations,
                spec: {
                  ...currentGroup.spec,
                  database: sharedDbMode ? { mode: sharedDbMode } : currentGroup.spec?.database,
                  services: priorServices,
                },
              },
            })
            break
          } catch (updateErr) {
            if (attempt < 2 && isAbortedConflictError(updateErr)) {
              if (!isCurrentSession()) return
              const fresh = await previewGroupClient.getPreviewGroup({
                namespace: currentGroup.namespace,
                name: currentGroup.name,
              })
              if (!isCurrentSession()) return
              if (fresh.previewGroup) {
                currentGroup = fresh.previewGroup
                continue
              }
            }
            throw updateErr
          }
        }

        if (!isCurrentSession()) return
        onClose()
        return
      }

      const firstCandidate = chosenCandidates[0]
      const resolvedHeaderKey = headerKey.trim() || DEFAULT_ROUTING_HEADER_KEY
      const resolvedHeaderVal = headerValue.trim() || effectiveName
      const resolvedExtUrl = appendPreviewEnvParam(externalUrl, resolvedHeaderVal)

      const orderedMembers: GroupMemberRecord[] = []
      for (const s of incomingSpecs) {
        if (s.mode === 'baseline' || !s.name) continue
        const rec = incomingMembers.get(s.name)
        if (rec) orderedMembers.push(rec)
      }

      const providerRaw = firstCandidate?.provider?.toLowerCase() ?? 'local'
      const validProvider = providerRaw === 'github' || providerRaw === 'gitlab' ? providerRaw : 'local'

      const res = await createGroup.mutateAsync({
        namespace: nsClean,
        previewGroup: {
          name: effectiveName,
          namespace: nsClean,
          labels: {
            [COMPOSED_GROUP_LABEL]: 'true',
          },
          annotations:
            orderedMembers.length > 0
              ? { [GROUP_MEMBERS_ANNOTATION]: serializeGroupMembers(orderedMembers) }
              : {},
          spec: {
            source: {
              provider: validProvider,
              project: firstCandidate?.project || undefined,
              mr: firstCandidate?.mr || undefined,
              branch: firstCandidate?.branch || effectiveName,
            },
            routing: {
              mode: 'header',
              headerKey: resolvedHeaderKey,
              headerValue: resolvedHeaderVal,
              externalUrl: resolvedExtUrl || undefined,
            },
            lifecycle: ttlSeconds
              ? {
                  ttl: { seconds: BigInt(parseInt(ttlSeconds, 10)) },
                  cleanupOnMerge: true,
                }
              : { cleanupOnMerge: true },
            database: sharedDbMode ? { mode: sharedDbMode } : undefined,
            clusterRef:
              trimmedCluster || trimmedSecret
                ? {
                    ...(trimmedCluster ? { name: trimmedCluster } : {}),
                    ...(trimmedSecret ? { kubeconfigSecretRef: { name: trimmedSecret } } : {}),
                  }
                : undefined,
            services: incomingSpecs,
          },
        },
      })

      if (!isCurrentSession()) return
      onClose()
      const created = res.previewGroup
      navigate(
        previewGroupPath({
          name: created?.name || effectiveName,
          namespace: created?.namespace ?? nsClean,
        }),
      )
    } catch (err) {
      if (!isCurrentSession()) return
      setError(err instanceof Error ? err.message : 'Failed to save preview group')
    } finally {
      if (sessionRef.current === submitSession) {
        submittingRef.current = false
        setIsSubmitting(false)
      }
    }
  }

  const isPending = isSubmitting || createGroup.isPending || updateGroup.isPending
  const nameInputId = `${formIdPrefix}-name`
  const nameHintId = `${formIdPrefix}-name-hint`
  const nsInputId = `${formIdPrefix}-namespace`
  const headerKeyId = `${formIdPrefix}-header-key`
  const headerValId = `${formIdPrefix}-header-val`
  const extUrlId = `${formIdPrefix}-ext-url`
  const dbModeId = `${formIdPrefix}-db-mode`
  const ttlId = `${formIdPrefix}-ttl`
  const clusterId = `${formIdPrefix}-cluster`
  const clusterSecId = `${formIdPrefix}-cluster-secret`
  const errId = `${formIdPrefix}-error`

  const candidateKeySet = useMemo(() => new Set(candidates.map((c) => c.key)), [candidates])
  const missingSelectedKeys = useMemo(
    () => selectedEnvKeys.filter((k) => !candidateKeySet.has(k)),
    [selectedEnvKeys, candidateKeySet],
  )
  const existingSharedDbMode = isJoinMode ? (existingGroup?.spec?.database?.mode?.trim() ?? '') : ''
  const dbModeOptions = useMemo(() => {
    if (!existingSharedDbMode) return DB_MODE_OPTIONS
    return DB_MODE_OPTIONS.map((opt) =>
      opt.value === ''
        ? { ...opt, label: `Keep current group database (${existingSharedDbMode})` }
        : opt,
    )
  }, [existingSharedDbMode])

  const hasNameError = !isJoinMode && Boolean(error && error.toLowerCase().includes('name'))

  return (
    <Dialog open={open} onClose={handleDismiss}>
      <DialogContent className="max-w-2xl max-h-[88vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {isJoinMode ? `Join Services to ${existingGroup?.name}` : 'Compose Multi-MR Preview Group'}
          </DialogTitle>
          <DialogDescription>
            {isJoinMode
              ? 'Attach open PR/MR environments or container images without affecting existing services in this group.'
              : 'Combine open backend, frontend, and worker MRs behind a single shared routing header and preview link.'}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          {!isJoinMode && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label htmlFor={nameInputId} className="text-sm font-medium mb-1 block">
                  Group Name <span aria-hidden="true">*</span>
                </label>
                <span id={nameHintId} className="text-xs text-muted-foreground block mb-1">
                  Lowercase letters, digits, and hyphens (shared header value).
                </span>
                <Input
                  id={nameInputId}
                  name="groupName"
                  value={name}
                  onChange={(e) => {
                    setError('')
                    setNameTouched(true)
                    setNameEditedManually(true)
                    setName(e.target.value)
                  }}
                  onBlur={handleNameBlur}
                  placeholder="feat-checkout"
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-describedby={hasNameError ? `${nameHintId} ${errId}` : nameHintId}
                  aria-invalid={hasNameError || undefined}
                  required
                />
              </div>
              <div>
                <label htmlFor={nsInputId} className="text-sm font-medium mb-1 block">
                  Default Namespace
                </label>
                <span className="text-xs text-muted-foreground block mb-1">
                  Default namespace for custom services and RBAC scope.
                </span>
                <Input
                  id={nsInputId}
                  name="namespace"
                  value={namespace}
                  onChange={(e) => {
                    setError('')
                    setNamespace(e.target.value)
                  }}
                  placeholder="default"
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                />
              </div>
            </div>
          )}

          <fieldset className="border rounded-md p-3 space-y-2">
            <legend className="text-sm font-medium px-1">
              Open PR / MR Environments ({candidates.length})
            </legend>
            {envsError && (
              <div className="flex items-center justify-between gap-2 rounded border border-destructive/30 bg-destructive/10 px-2.5 py-2 text-xs text-destructive">
                <span>
                  Failed to load preview environments: {envsError instanceof Error ? envsError.message : 'Unknown error'}
                </span>
                <Button type="button" variant="outline" size="sm" onClick={() => refetchEnvs()}>
                  Retry
                </Button>
              </div>
            )}
            {envsLoading && candidates.length === 0 ? (
              <p className="text-xs text-muted-foreground">Loading standalone preview environments...</p>
            ) : !envsError && candidates.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No standalone preview environments with container images found. Add custom service images below.
              </p>
            ) : candidates.length > 0 ? (
              <div className="space-y-2 max-h-44 overflow-y-auto pr-1">
                {candidates.map((cand) => {
                  const cbId = `${formIdPrefix}-env-${encodeURIComponent(cand.key)}`
                  const checked = selectedEnvKeys.includes(cand.key)
                  return (
                    <div
                      key={cand.key}
                      className="flex items-start gap-2.5 rounded border border-border/60 px-2.5 py-2 text-xs"
                    >
                      <input
                        type="checkbox"
                        id={cbId}
                        name="selectedEnvironments"
                        value={cand.key}
                        checked={checked}
                        onChange={() => toggleCandidate(cand.key, cand.branch)}
                        className="mt-0.5 h-4 w-4 accent-primary"
                      />
                      <label htmlFor={cbId} className="flex-1 cursor-pointer space-y-0.5">
                        <div className="flex flex-wrap items-center gap-1.5 font-medium text-foreground">
                          <span className="font-mono">{cand.serviceName}</span>
                          <Badge variant="secondary" className="text-[10px] font-mono">
                            {cand.namespace}
                          </Badge>
                          {cand.project && cand.mr > 0 && (
                            <Badge variant="outline" className="text-[10px] font-mono">
                              {cand.project}#{cand.mr}
                            </Badge>
                          )}
                          {cand.branch && (
                            <span className="text-muted-foreground font-mono">({cand.branch})</span>
                          )}
                        </div>
                        <div className="text-muted-foreground font-mono break-all">
                          {cand.image}
                        </div>
                      </label>
                    </div>
                  )
                })}
              </div>
            ) : null}
            {missingSelectedKeys.length > 0 && (
              <div className="space-y-1.5 pt-1 border-t border-border/60">
                <p className="text-xs text-destructive font-medium">
                  Unavailable selections (no longer present):
                </p>
                {missingSelectedKeys.map((missingKey) => (
                  <div
                    key={missingKey}
                    className="flex items-center justify-between gap-2 rounded border border-destructive/30 bg-destructive/5 px-2.5 py-1.5 text-xs"
                  >
                    <span className="font-mono">{missingKey}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`Remove unavailable selection ${missingKey}`}
                      onClick={() => {
                        setError('')
                        setSelectedEnvKeys((prev) => prev.filter((k) => k !== missingKey))
                      }}
                    >
                      Remove
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </fieldset>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Custom / Additional Services</span>
              <Button type="button" variant="outline" size="sm" onClick={addCustomRow}>
                <Plus className="h-3.5 w-3.5 mr-1" aria-hidden="true" />
                Add Custom Service
              </Button>
            </div>
            {customServices.map((row, idx) => {
              const svcNameId = `${formIdPrefix}-svc-name-${row.id}`
              const svcModeId = `${formIdPrefix}-svc-mode-${row.id}`
              const svcImgId = `${formIdPrefix}-svc-img-${row.id}`
              const svcNsId = `${formIdPrefix}-svc-ns-${row.id}`
              return (
                <div
                  key={row.id}
                  className="grid grid-cols-1 sm:grid-cols-12 gap-2 items-end border rounded-md p-2.5"
                >
                  <div className="sm:col-span-3">
                    <label htmlFor={svcNameId} className="text-xs font-medium mb-1 block">
                      Service #{idx + 1}
                    </label>
                    <Input
                      id={svcNameId}
                      name={`serviceName-${idx}`}
                      value={row.name}
                      onChange={(e) => updateCustomRow(row.id, { name: e.target.value })}
                      placeholder="payments-api"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                    />
                  </div>
                  <div className="sm:col-span-2">
                    <label htmlFor={svcModeId} className="text-xs font-medium mb-1 block">
                      Mode
                    </label>
                    <Select
                      id={svcModeId}
                      name={`serviceMode-${idx}`}
                      value={row.mode}
                      onChange={(e) =>
                        updateCustomRow(row.id, { mode: e.target.value as 'image' | 'baseline' })
                      }
                    >
                      <option value="image">Image</option>
                      <option value="baseline">Baseline</option>
                    </Select>
                  </div>
                  <div className="sm:col-span-4">
                    <label htmlFor={svcImgId} className="text-xs font-medium mb-1 block">
                      Container Image
                    </label>
                    <Input
                      id={svcImgId}
                      name={`serviceImage-${idx}`}
                      value={row.image}
                      disabled={row.mode === 'baseline'}
                      onChange={(e) => updateCustomRow(row.id, { image: e.target.value })}
                      placeholder={row.mode === 'baseline' ? 'Uses baseline pod' : 'ghcr.io/acme/payments:pr-42'}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                    />
                  </div>
                  <div className="sm:col-span-2">
                    <label htmlFor={svcNsId} className="text-xs font-medium mb-1 block">
                      Namespace
                    </label>
                    <Input
                      id={svcNsId}
                      name={`serviceNamespace-${idx}`}
                      value={row.namespace}
                      onChange={(e) => updateCustomRow(row.id, { namespace: e.target.value })}
                      placeholder="default"
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                    />
                  </div>
                  <div className="sm:col-span-1 flex justify-end">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={`Remove custom service ${row.name || idx + 1}`}
                      onClick={() => removeCustomRow(row.id)}
                    >
                      <Trash2 className="h-4 w-4" aria-hidden="true" />
                    </Button>
                  </div>
                </div>
              )
            })}
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label htmlFor={dbModeId} className="text-sm font-medium mb-1 block">
                Database Isolation
              </label>
              <Select
                id={dbModeId}
                name="sharedDatabaseMode"
                value={sharedDbMode}
                onChange={(e) => {
                  setError('')
                  setSharedDbMode(e.target.value)
                }}
              >
                {dbModeOptions.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
            {!isJoinMode && (
              <div>
                <label htmlFor={ttlId} className="text-sm font-medium mb-1 block">
                  TTL
                </label>
                <Select
                  id={ttlId}
                  name="ttl"
                  value={ttlSeconds}
                  onChange={(e) => {
                    setError('')
                    setTtlSeconds(e.target.value)
                  }}
                >
                  {TTL_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </Select>
              </div>
            )}
          </div>

          {!isJoinMode && (
            <>
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                <div>
                  <label htmlFor={headerKeyId} className="text-sm font-medium mb-1 block">
                    Header Key
                  </label>
                  <Input
                    id={headerKeyId}
                    name="headerKey"
                    value={headerKey}
                    onChange={(e) => {
                      setError('')
                      setHeaderKey(e.target.value)
                    }}
                    placeholder="x-diverge-env"
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                  />
                </div>
                <div>
                  <label htmlFor={headerValId} className="text-sm font-medium mb-1 block">
                    Header Value
                  </label>
                  <Input
                    id={headerValId}
                    name="headerValue"
                    value={headerValue}
                    onChange={(e) => {
                      setError('')
                      setHeaderValue(e.target.value)
                    }}
                    placeholder={name.trim() || 'Defaults to group name'}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                  />
                </div>
                <div>
                  <label htmlFor={extUrlId} className="text-sm font-medium mb-1 block">
                    Staging Base URL
                  </label>
                  <Input
                    id={extUrlId}
                    name="externalUrl"
                    value={externalUrl}
                    onChange={(e) => {
                      setError('')
                      setExternalUrl(e.target.value)
                    }}
                    placeholder="https://staging.example.com"
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div>
                  <label htmlFor={clusterId} className="text-sm font-medium mb-1 block">
                    Target Cluster
                  </label>
                  <Input
                    id={clusterId}
                    name="targetCluster"
                    value={targetCluster}
                    onChange={(e) => {
                      setError('')
                      setTargetCluster(e.target.value)
                    }}
                    placeholder="local (optional spoke)"
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                  />
                </div>
                <div>
                  <label htmlFor={clusterSecId} className="text-sm font-medium mb-1 block">
                    Spoke Kubeconfig Secret
                  </label>
                  <Input
                    id={clusterSecId}
                    name="clusterSecret"
                    value={clusterSecret}
                    onChange={(e) => {
                      setError('')
                      setClusterSecret(e.target.value)
                    }}
                    placeholder="spoke-kubeconfig"
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                  />
                </div>
              </div>
            </>
          )}

          {error && (
            <p id={errId} role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={handleDismiss}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending
                ? isJoinMode
                  ? 'Joining...'
                  : 'Composing...'
                : isJoinMode
                  ? 'Join Services'
                  : 'Compose Preview Group'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
