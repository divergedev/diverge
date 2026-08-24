import { render, screen, cleanup } from '../test/utils'
import { TTLCountdown } from './TTLCountdown'
import fc from 'fast-check'

describe('TTLCountdown Properties', () => {
  afterEach(cleanup)

  it('any future date -> shows positive countdown', () => {
    fc.assert(
      fc.property(fc.integer({ min: 1000, max: 864000000 }), (diff) => {
        const expiresAt = new Date(Date.now() + diff).toISOString()
        render(<TTLCountdown expiresAt={expiresAt} />)
        expect(screen.queryByText('Expired')).not.toBeInTheDocument()
        expect(screen.queryByText('No TTL')).not.toBeInTheDocument()
        cleanup()
      })
    )
  })

  it('any past date -> shows Expired', () => {
    fc.assert(
      fc.property(fc.integer({ min: 1000, max: 864000000 }), (diff) => {
        const expiresAt = new Date(Date.now() - diff).toISOString()
        render(<TTLCountdown expiresAt={expiresAt} />)
        expect(screen.getByText('Expired')).toBeInTheDocument()
        cleanup()
      })
    )
  })
})
