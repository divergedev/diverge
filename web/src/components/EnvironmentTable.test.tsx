import { render, screen } from '../test/utils'
import { EnvironmentTable } from './EnvironmentTable'
import { createMockEnvironment } from '../test/mocks/factories'

describe('EnvironmentTable', () => {
  const env = createMockEnvironment()

  it('Renders table headers', () => {
    render(<EnvironmentTable environments={[]} isLoading={false} />)
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
  })

  it('Renders environment rows with correct data', () => {
    render(<EnvironmentTable environments={[env]} isLoading={false} />)
    expect(screen.getByText('test-env')).toBeInTheDocument()
  })
})
