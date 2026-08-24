import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { environmentClient, previewGroupClient, clusterClient, authClient } from '@/api/client'

export const queryKeys = {
  environments: (ns?: string) => ['environments', ns ?? 'all'] as const,
  environment: (ns: string, name: string) => ['environment', ns, name] as const,
  previewGroups: (ns?: string) => ['previewGroups', ns ?? 'all'] as const,
  previewGroup: (ns: string, name: string) => ['previewGroup', ns, name] as const,
  clusterInfo: ['clusterInfo'] as const,
  currentUser: ['currentUser'] as const,
  permissions: (ns?: string) => ['permissions', ns ?? 'all'] as const,
}

export function useListEnvironments(params: { namespace?: string; phase?: string; pageSize?: number; pageToken?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.environments(params.namespace),
    queryFn: () => environmentClient.listEnvironments({
      namespace: params.namespace ?? '',
      phase: params.phase ?? '',
      pageSize: params.pageSize ?? 100,
      pageToken: params.pageToken ?? '',
    }),
  })
}

export function useGetEnvironment(namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.environment(namespace, name),
    queryFn: () => environmentClient.getEnvironment({ namespace, name }),
    enabled: !!name && !!namespace,
  })
}

export function useListPreviewGroups(params: { namespace?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.previewGroups(params.namespace),
    queryFn: () => previewGroupClient.listPreviewGroups({ namespace: params.namespace ?? '' }),
  })
}

export function useGetPreviewGroup(namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.previewGroup(namespace, name),
    queryFn: () => previewGroupClient.getPreviewGroup({ namespace, name }),
    enabled: !!name && !!namespace,
  })
}

export function useGetClusterInfo() {
  return useQuery({
    queryKey: queryKeys.clusterInfo,
    queryFn: () => clusterClient.getClusterInfo({}),
  })
}

export function useGetCurrentUser() {
  return useQuery({
    queryKey: queryKeys.currentUser,
    queryFn: () => authClient.getCurrentUser({}),
    retry: false,
  })
}

export function useListPermissions(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.permissions(namespace),
    queryFn: () => authClient.listPermissions({ namespace: namespace ?? '' }),
  })
}

export function useCreateEnvironment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { name: string; namespace: string; spec: Record<string, unknown> }) =>
      environmentClient.createEnvironment(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['environments'] }) },
  })
}

export function useDeleteEnvironment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string }) =>
      environmentClient.deleteEnvironment(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['environments'] }) },
  })
}

export function useExtendTTL() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string; duration: string }) =>
      environmentClient.extendTTL(req),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.environment(vars.namespace, vars.name) })
    },
  })
}

export function useCreatePreviewGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { name: string; namespace: string; spec: Record<string, unknown> }) =>
      previewGroupClient.createPreviewGroup(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['previewGroups'] }) },
  })
}

export function useDeletePreviewGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string }) =>
      previewGroupClient.deletePreviewGroup(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['previewGroups'] }) },
  })
}
