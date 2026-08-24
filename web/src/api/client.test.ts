import { getToken, setToken, clearToken } from './client'

describe('client', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('gets token correctly', () => {
    localStorage.setItem('diverge:token', 'foo')
    expect(getToken()).toBe('foo')
  })

  it('sets token correctly', () => {
    setToken('bar')
    expect(localStorage.getItem('diverge:token')).toBe('bar')
  })

  it('clears token correctly', () => {
    localStorage.setItem('diverge:token', 'foo')
    clearToken()
    expect(localStorage.getItem('diverge:token')).toBeNull()
  })
})
