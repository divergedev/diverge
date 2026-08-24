import { Environment, EnvironmentStatus, EnvironmentSpec, EnvironmentSource } from '@/api/gen/diverge/v1alpha1/environment_pb'

export const createMockEnvironment = (overrides?: { name?: string; namespace?: string; phase?: string }): Environment => {
  const name = overrides?.name ?? 'test-env'
  const namespace = overrides?.namespace ?? 'default'
  const phase = overrides?.phase ?? 'Ready'

  return new Environment({
    name,
    namespace,
    spec: new EnvironmentSpec({
      source: new EnvironmentSource({
        branch: 'feature/test',
      }),
    }),
    status: new EnvironmentStatus({
      phase,
      url: `https://${name}.preview.example.com`,
    }),
  })
}
