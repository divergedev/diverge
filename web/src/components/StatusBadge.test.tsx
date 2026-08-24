import { render, screen } from '../test/utils'
import { StatusBadge } from './StatusBadge'

describe('StatusBadge', () => {
  it('renders correct text for each known phase', () => {
    render(<StatusBadge phase="Ready" />)
    expect(screen.getByText('Ready')).toBeInTheDocument()
  })

  it('renders "Unknown" for empty phase', () => {
    render(<StatusBadge phase="" />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('known phases get specific CSS classes', () => {
    render(<StatusBadge phase="Ready" />)
    const badge = screen.getByText('Ready')
    expect(badge).toHaveClass('bg-green-500/20')
  })

  it('unknown phases get gray styling', () => {
    render(<StatusBadge phase="Blah" />)
    const badge = screen.getByText('Blah')
    expect(badge).toHaveClass('bg-gray-500/20')
  })
})
