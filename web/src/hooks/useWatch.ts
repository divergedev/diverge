import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { environmentClient, previewGroupClient, getToken } from '@/api/client'
import { queryKeys } from '@/api/queries'
import type { Environment } from '@/api/gen/diverge/v1alpha1/environment_pb'
import type { PreviewGroup } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import type { ListEnvironmentsResponse } from '@/api/gen/diverge/v1alpha1/environment_pb'
import type { ListPreviewGroupsResponse } from '@/api/gen/diverge/v1alpha1/previewgroup_pb'
import { WatchEventType } from '@/api/gen/diverge/v1alpha1/types_pb'

function backoff(attempt: number): number {
  const base = Math.min(1000 * Math.pow(2, attempt), 30000)
  return base + Math.random() * 1000
}

export function useWatchEnvironments(namespace?: string) {
  const qc = useQueryClient()
  const attemptRef = useRef(0)

  useEffect(() => {
    if (!getToken()) return

    let aborted = false
    const ac = new AbortController()

    async function watch() {
      while (!aborted) {
        try {
          for await (const msg of environmentClient.watchEnvironments(
            { namespace: namespace ?? '' },
            { signal: ac.signal },
          )) {
            attemptRef.current = 0
            const env = msg.environment
            if (!env) continue

            qc.setQueryData(
              queryKeys.environments(namespace),
              (old: ListEnvironmentsResponse | undefined) => {
                if (!old) return old
                const list = [...old.environments]
                const idx = list.findIndex(
                  (e: Environment) => e.name === env.name && e.namespace === env.namespace,
                )
                if (msg.type === WatchEventType.DELETED) {
                  if (idx >= 0) list.splice(idx, 1)
                } else {
                  if (idx >= 0) list[idx] = env
                  else list.unshift(env)
                }
                return { ...old, environments: list }
              },
            )
          }
        } catch (err) {
          if (aborted) return
          const delay = backoff(attemptRef.current++)
          console.warn(`[watch-env] reconnecting in ${Math.round(delay)}ms`, err)
          await new Promise((r) => setTimeout(r, delay))
        }
      }
    }

    watch()
    return () => { aborted = true; ac.abort() }
  }, [namespace, qc])
}

export function useWatchPreviewGroups(namespace?: string) {
  const qc = useQueryClient()
  const attemptRef = useRef(0)

  useEffect(() => {
    if (!getToken()) return

    let aborted = false
    const ac = new AbortController()

    async function watch() {
      while (!aborted) {
        try {
          for await (const msg of previewGroupClient.watchPreviewGroups(
            { namespace: namespace ?? '' },
            { signal: ac.signal },
          )) {
            attemptRef.current = 0
            const pg = msg.previewGroup
            if (!pg) continue

            qc.setQueryData(
              queryKeys.previewGroups(namespace),
              (old: ListPreviewGroupsResponse | undefined) => {
                if (!old) return old
                const list = [...old.previewGroups]
                const idx = list.findIndex(
                  (p: PreviewGroup) => p.name === pg.name && p.namespace === pg.namespace,
                )
                if (msg.type === WatchEventType.DELETED) {
                  if (idx >= 0) list.splice(idx, 1)
                } else {
                  if (idx >= 0) list[idx] = pg
                  else list.unshift(pg)
                }
                return { ...old, previewGroups: list }
              },
            )
          }
        } catch (err) {
          if (aborted) return
          const delay = backoff(attemptRef.current++)
          console.warn(`[watch-pg] reconnecting in ${Math.round(delay)}ms`, err)
          await new Promise((r) => setTimeout(r, delay))
        }
      }
    }

    watch()
    return () => { aborted = true; ac.abort() }
  }, [namespace, qc])
}
