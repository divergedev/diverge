import { setToken, getToken, clearToken } from './client'
import fc from 'fast-check'

describe('client PBT', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('can store and retrieve any valid token string', () => {
    fc.assert(
      fc.property(fc.string(), (token) => {
        setToken(token)
        expect(getToken()).toBe(token)
        clearToken()
      })
    )
  })
})
