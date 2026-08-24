import { render, screen } from '../test/utils'
import { EmptyState } from './EmptyState'
import { Box } from 'lucide-react'

describe('EmptyState', () => {
  it('renders icon, title, description', () => {
    render(<EmptyState icon={Box} title="Test Title" description="Test Description" />)
    expect(screen.getByText('Test Title')).toBeInTheDocument()
    expect(screen.getByText('Test Description')).toBeInTheDocument()
  })

  it('renders optional action button', () => {
    render(<EmptyState icon={Box} title="Test Title" description="Test Description" action={<button>Click Me</button>} />)
    expect(screen.getByRole('button', { name: 'Click Me' })).toBeInTheDocument()
  })
})
