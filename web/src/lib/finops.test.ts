import { describe, it, expect } from 'vitest'
import {
  isEnvironmentHibernated,
  estimateEnvironmentHourlyCostUSD,
  formatHourlyCostUSD,
  buildMagicPreviewUrl,
  parseCpuCores,
  parseMemoryGiB,
} from './finops'

describe('finops utilities', () => {
  describe('isEnvironmentHibernated', () => {
    it('returns true when divergedev.com/hibernated annotation is "true"', () => {
      expect(
        isEnvironmentHibernated({
          name: 'pr-1',
          annotations: { 'divergedev.com/hibernated': 'true' },
          status: { phase: 'Ready' },
        }),
      ).toBe(true)
    })

    it('returns true when status phase is Hibernated (case-insensitive)', () => {
      expect(
        isEnvironmentHibernated({
          name: 'pr-1',
          status: { phase: 'Hibernated' },
        }),
      ).toBe(true)
      expect(
        isEnvironmentHibernated({
          name: 'pr-1',
          status: { phase: 'hibernated' },
        }),
      ).toBe(true)
    })

    it('returns false for active environments', () => {
      expect(
        isEnvironmentHibernated({
          name: 'pr-1',
          annotations: { 'divergedev.com/hibernated': 'false' },
          status: { phase: 'Ready' },
        }),
      ).toBe(false)
    })
  })

  describe('estimateEnvironmentHourlyCostUSD', () => {
    it('returns default pod cost ($0.012) when no resources or database are specified', () => {
      const cost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-1',
        status: { phase: 'Ready' },
      })
      expect(cost).toBeCloseTo(0.012, 6)
      expect(formatHourlyCostUSD(cost)).toBe('$0.012/hr')
    })

    it('adds schema database cost (+$0.010 = $0.022)', () => {
      const cost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-2',
        spec: {
          database: { mode: 'schema' },
        },
        status: { phase: 'Ready' },
      })
      expect(cost).toBeCloseTo(0.022, 6)
      expect(formatHourlyCostUSD(cost)).toBe('$0.022/hr')
    })

    it('adds cnpg/neon and snapshot/fresh database costs accurately (+$0.080 = $0.092)', () => {
      const cnpgCost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-cnpg',
        spec: { database: { mode: 'cnpg' } },
        status: { phase: 'Ready' },
      })
      expect(cnpgCost).toBeCloseTo(0.092, 6)

      const snapshotCost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-snap',
        spec: { database: { mode: 'snapshot' } },
        status: { phase: 'Ready' },
      })
      expect(snapshotCost).toBeCloseTo(0.092, 6)
    })

    it('computes explicit CPU (500m) and memory (1Gi) requests with replicas', () => {
      const singleReplicaCost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-explicit',
        spec: {
          deploy: {
            replicas: 1,
            resources: {
              cpuRequest: '500m',
              memoryRequest: '1Gi',
            },
          },
        },
        status: { phase: 'Ready' },
      })
      // 0.5 * 0.031611 + 1 * 0.004237 = 0.0158055 + 0.004237 = 0.0200425
      expect(singleReplicaCost).toBeCloseTo(0.0200425, 6)

      const twoReplicasCost = estimateEnvironmentHourlyCostUSD({
        name: 'pr-explicit-2',
        spec: {
          deploy: {
            replicas: 2,
            resources: {
              cpuRequest: '500m',
              memoryRequest: '1Gi',
            },
          },
        },
        status: { phase: 'Ready' },
      })
      expect(twoReplicasCost).toBeCloseTo(0.040085, 6)
    })

    it('returns $0 compute cost when environment is hibernated', () => {
      const hibernatedNoDb = estimateEnvironmentHourlyCostUSD({
        name: 'pr-hibernated',
        annotations: { 'divergedev.com/hibernated': 'true' },
        status: { phase: 'Ready' },
      })
      expect(hibernatedNoDb).toBe(0)
      expect(formatHourlyCostUSD(hibernatedNoDb)).toBe('$0.000/hr')

      const hibernatedWithSchemaDb = estimateEnvironmentHourlyCostUSD({
        name: 'pr-hibernated-db',
        annotations: { 'divergedev.com/hibernated': 'true' },
        spec: { database: { mode: 'schema' } },
        status: { phase: 'Ready' },
      })
      expect(hibernatedWithSchemaDb).toBeCloseTo(0.01, 6)
    })

    it('returns 0 when phase is Failed or Terminating', () => {
      expect(
        estimateEnvironmentHourlyCostUSD({
          name: 'pr-failed',
          spec: { database: { mode: 'cnpg' } },
          status: { phase: 'Failed' },
        }),
      ).toBe(0)

      expect(
        estimateEnvironmentHourlyCostUSD({
          name: 'pr-terminating',
          spec: { database: { mode: 'cnpg' } },
          status: { phase: 'Terminating' },
        }),
      ).toBe(0)
    })
  })

  describe('parseCpuCores and parseMemoryGiB', () => {
    it('parses millicores and float cores', () => {
      expect(parseCpuCores('500m')).toBeCloseTo(0.5, 6)
      expect(parseCpuCores('2')).toBeCloseTo(2, 6)
      expect(parseCpuCores('invalid')).toBe(0)
    })

    it('parses Ki, Mi, Gi, Ti memory units into GiB', () => {
      expect(parseMemoryGiB('1Gi')).toBeCloseTo(1, 6)
      expect(parseMemoryGiB('512Mi')).toBeCloseTo(0.5, 6)
      expect(parseMemoryGiB('1048576Ki')).toBeCloseTo(1, 6)
      expect(parseMemoryGiB('1Ti')).toBeCloseTo(1024, 6)
    })
  })

  describe('buildMagicPreviewUrl', () => {
    it('appends ?_diverge_env=<headerValue || name> for valid external URLs', () => {
      const urlWithName = buildMagicPreviewUrl({
        name: 'pr-42',
        status: { url: 'https://staging.example.com/app' },
      })
      expect(urlWithName).toBe('https://staging.example.com/app?_diverge_env=pr-42')

      const urlWithHeaderValue = buildMagicPreviewUrl({
        name: 'pr-42',
        spec: { routing: { headerValue: 'custom-env-42' } },
        status: { url: 'https://staging.example.com/app?foo=bar' },
      })
      expect(urlWithHeaderValue).toBe('https://staging.example.com/app?foo=bar&_diverge_env=custom-env-42')
    })

    it('returns undefined when status.url is missing or unsafe', () => {
      expect(buildMagicPreviewUrl({ name: 'pr-42' })).toBeUndefined()
      expect(
        buildMagicPreviewUrl({
          name: 'pr-42',
          status: { url: 'javascript:alert(1)' },
        }),
      ).toBeUndefined()
    })
  })
})
