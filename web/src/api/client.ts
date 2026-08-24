import { createPromiseClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import type { Interceptor } from '@connectrpc/connect'
import { EnvironmentService } from '@/api/gen/diverge/v1alpha1/environment_connect'
import { PreviewGroupService } from '@/api/gen/diverge/v1alpha1/previewgroup_connect'
import { ClusterService } from '@/api/gen/diverge/v1alpha1/cluster_connect'
import { AuthService } from '@/api/gen/diverge/v1alpha1/auth_connect'

const TOKEN_KEY = 'diverge:token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = getToken()
  if (token) {
    req.header.set('Authorization', `Bearer ${token}`)
  }
  return next(req)
}

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  interceptors: [authInterceptor],
})

export const environmentClient = createPromiseClient(EnvironmentService, transport)
export const previewGroupClient = createPromiseClient(PreviewGroupService, transport)
export const clusterClient = createPromiseClient(ClusterService, transport)
export const authClient = createPromiseClient(AuthService, transport)
