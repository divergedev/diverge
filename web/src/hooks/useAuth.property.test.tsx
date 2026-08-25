import { renderHook, act } from '../test/utils'
import { useAuth } from './useAuth'
import { server } from '../test/mocks/server'
import { http, HttpResponse } from 'msw'
import fc from 'fast-check'
import { getToken } from '@/api/client'

describe('useAuth PBT', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('any token string can be stored and retrieved', async () => {
    // Override the mock to allow any token for this property test
    server.use(
      http.post('*/diverge.v1alpha1.AuthService/GetCurrentUser', () => {
        return HttpResponse.json({
          userId: 'pbt-user',
          username: 'pbt',
          email: 'pbt@example.com',
          groups: [],
          issuer: 'pbt'
        })
      })
    )

    await fc.assert(
      fc.asyncProperty(fc.string(), async (token) => {
        const { result } = renderHook(() => useAuth())

        let success = false
        await act(async () => {
          success = await result.current.login(token)
        })

        expect(success).toBe(true)
        expect(getToken()).toBe(token)
        expect(result.current.token).toBe(token)

        await act(async () => {
          result.current.logout()
        })

        expect(getToken()).toBeNull()
      }),
      { numRuns: 20 }
    )
  })
})
