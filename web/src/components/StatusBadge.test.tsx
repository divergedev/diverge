import { render, screen } from '../test/utils'
import { StatusBadge, formatAgentTaskPhase } from './StatusBadge'
import { AgentTaskPhase } from '@/api/gen/diverge/v1alpha1/agenttask_pb'

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

  it('renders pulsing amber for Migrating and Pausing phases', () => {
    const { rerender } = render(<StatusBadge phase="Migrating" />)
    expect(screen.getByText('Migrating')).toHaveClass('bg-amber-500/20', 'animate-pulse')

    rerender(<StatusBadge phase="Pausing" />)
    expect(screen.getByText('Pausing')).toHaveClass('bg-amber-500/20', 'animate-pulse')
  })

  it('unknown phases get gray styling', () => {
    render(<StatusBadge phase="Blah" />)
    const badge = screen.getByText('Blah')
    expect(badge).toHaveClass('bg-gray-500/20')
  })

  it('maps all AgentTaskPhase enum values via formatAgentTaskPhase', () => {
    expect(formatAgentTaskPhase(AgentTaskPhase.PENDING)).toBe('Pending')
    expect(formatAgentTaskPhase(AgentTaskPhase.PROVISIONING)).toBe('Provisioning')
    expect(formatAgentTaskPhase(AgentTaskPhase.ACTIVE)).toBe('Active')
    expect(formatAgentTaskPhase(AgentTaskPhase.PAUSING)).toBe('Pausing')
    expect(formatAgentTaskPhase(AgentTaskPhase.PAUSED)).toBe('Paused')
    expect(formatAgentTaskPhase(AgentTaskPhase.COMPLETED)).toBe('Completed')
    expect(formatAgentTaskPhase(AgentTaskPhase.FAILED)).toBe('Failed')
    expect(formatAgentTaskPhase(AgentTaskPhase.UNSPECIFIED)).toBe('Unknown')
    expect(formatAgentTaskPhase(undefined)).toBe('Unknown')
  })
})
