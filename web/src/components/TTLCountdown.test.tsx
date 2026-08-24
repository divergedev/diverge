import { render, screen } from '../test/utils'
import { TTLCountdown } from './TTLCountdown'

describe('TTLCountdown', () => {
  it('Shows "No TTL" when expiresAt is undefined', () => {
    render(<TTLCountdown />)
    expect(screen.getByText('No TTL')).toBeInTheDocument()
  })

  it('Shows "Expired" when date is in the past', () => {
    render(<TTLCountdown expiresAt={new Date(Date.now() - 10000).toISOString()} />)
    expect(screen.getByText('Expired')).toBeInTheDocument()
  })

  it('Shows correct format for hours remaining', () => {
    render(<TTLCountdown expiresAt={new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString()} />)
    expect(screen.getByText(/h\s*\d+m/)).toBeInTheDocument()
  })

  it('Shows correct format for minutes remaining', () => {
    render(<TTLCountdown expiresAt={new Date(Date.now() + 15 * 60 * 1000).toISOString()} />)
    expect(screen.getByText(/m\s*\d+s/)).toBeInTheDocument()
  })
})
