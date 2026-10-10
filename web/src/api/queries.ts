import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import type { PartialMessage } from '@bufbuild/protobuf'
import type { AgentTaskSpec } from '@/api/gen/diverge/v1alpha1/agenttask_pb'
import { environmentClient, previewGroupClient, clusterClient, authClient, agentTaskClient } from '@/api/client'

export const queryKeys = {
  environments: (ns?: string) => ['environments', ns ?? 'all'] as const,
  environment: (ns: string, name: string) => ['environment', ns, name] as const,
  previewGroups: (ns?: string) => ['previewGroups', ns ?? 'all'] as const,
  previewGroup: (ns: string, name: string) => ['previewGroup', ns, name] as const,
  agentTasks: (ns?: string) => ['agentTasks', ns ?? 'all'] as const,
  agentTask: (ns: string, name: string) => ['agentTask', ns, name] as const,
  hookJobs: (ns: string, envName: string) => ['hookJobs', ns, envName] as const,
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

export function useListHookJobs(namespace: string, environmentName: string) {
  return useQuery({
    queryKey: queryKeys.hookJobs(namespace, environmentName),
    queryFn: () => environmentClient.listHookJobs({ namespace, environmentName }),
    enabled: !!namespace && !!environmentName,
    refetchInterval: 5000,
  })
}

export function useRetryHook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; environmentName: string; hookType: string }) =>
      environmentClient.retryHook(req),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.hookJobs(vars.namespace, vars.environmentName) })
    },
  })
}

export function useListAgentTasks(params: { namespace?: string; pageSize?: number; pageToken?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.agentTasks(params.namespace),
    queryFn: () => agentTaskClient.listTasks({
      namespace: params.namespace ?? '',
      pagination: {
        pageSize: params.pageSize ?? 100,
        pageToken: params.pageToken ?? '',
      },
    }),
    refetchInterval: 5000,
  })
}

export function useGetAgentTask(namespace: string, name: string) {
  return useQuery({
    queryKey: queryKeys.agentTask(namespace, name),
    queryFn: () => agentTaskClient.getTask({ namespace, name }),
    enabled: !!name && !!namespace,
    refetchInterval: 5000,
  })
}

export function useCreateAgentTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { name: string; namespace: string; spec: PartialMessage<AgentTaskSpec> }) =>
      agentTaskClient.createTask(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['agentTasks'] }) },
  })
}

export function useDeleteAgentTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string }) =>
      agentTaskClient.deleteTask(req),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['agentTasks'] }) },
  })
}

export function usePauseAgentTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string }) =>
      agentTaskClient.pauseTask(req),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.agentTask(vars.namespace, vars.name) })
      qc.invalidateQueries({ queryKey: ['agentTasks'] })
    },
  })
}

export function useResumeAgentTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string; budgetUsdBump?: string; budgetTokensBump?: bigint }) =>
      agentTaskClient.resumeTask(req),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.agentTask(vars.namespace, vars.name) })
      qc.invalidateQueries({ queryKey: ['agentTasks'] })
    },
  })
}

export function useGuideAgentTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: { namespace: string; name: string; message: string }) =>
      agentTaskClient.guideTask(req),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.agentTask(vars.namespace, vars.name) })
    },
  })
}
