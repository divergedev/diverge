import { http, HttpResponse } from 'msw'

// Raw JSON responses to avoid protobuf serialization issues in test env.
// ConnectRPC JSON format uses camelCase field names.

const mockUser = {
  userId: 'usr-123',
  username: 'test-user',
  email: 'test@example.com',
  groups: ['developers'],
  issuer: 'test',
}

const mockEnvironment = (name = 'test-env') => ({
  name,
  namespace: 'default',
  spec: {
    source: { branch: 'feature/test' },
    lifecycle: { ttl: '86400s' },
  },
  status: {
    phase: 'Ready',
    url: `https://${name}.preview.example.com`,
    services: [],
  },
})

export const handlers = [
  http.post('*/diverge.v1alpha1.AuthService/GetCurrentUser', ({ request }) => {
    const auth = request.headers.get('Authorization')
    if (auth === 'Bearer invalid-token') {
      return new HttpResponse(null, { status: 401 })
    }
    return HttpResponse.json(mockUser)
  }),
  http.post('*/diverge.v1alpha1.EnvironmentService/ListEnvironments', () => {
    return HttpResponse.json({
      environments: [mockEnvironment('test-env'), mockEnvironment('prod-env')],
    })
  }),
  http.post('*/diverge.v1alpha1.EnvironmentService/CreateEnvironment', async ({ request }) => {
    const body = await request.json() as Record<string, unknown>
    const name = (body as { name?: string }).name ?? 'new-env'
    return HttpResponse.json({
      environment: mockEnvironment(name),
    })
  }),
  http.post('*/diverge.v1alpha1.EnvironmentService/GetEnvironment', () => {
    return HttpResponse.json({
      environment: mockEnvironment('test-env'),
    })
  }),
  http.post('*/diverge.v1alpha1.EnvironmentService/DeleteEnvironment', () => {
    return HttpResponse.json({})
  }),
  http.post('*/diverge.v1alpha1.EnvironmentService/ExtendTTL', () => {
    return HttpResponse.json({
      environment: mockEnvironment('test-env'),
    })
  }),
  http.post('*/diverge.v1alpha1.PreviewGroupService/ListPreviewGroups', () => {
    return HttpResponse.json({ previewGroups: [] })
  }),
  http.post('*/diverge.v1alpha1.ClusterService/GetClusterInfo', () => {
    return HttpResponse.json({
      version: 'v0.1.0',
      environmentCount: 2,
      previewGroupCount: 0,
      healthy: true,
      namespaces: ['default', 'staging'],
    })
  }),
]
