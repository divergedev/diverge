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

  it('Renders Drifted badge when BaselineDriftDetected condition is True', () => {
    const driftedEnv = createMockEnvironment({ name: 'drifted-env' })
    driftedEnv.status?.conditions.push({
      type: 'BaselineDriftDetected',
      status: 'True',
    } as never)
    render(<EnvironmentTable environments={[driftedEnv]} isLoading={false} />)
    expect(screen.getByText('Drifted')).toBeInTheDocument()
  })
})
