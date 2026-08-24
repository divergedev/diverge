import { render, screen } from '../test/utils'
import { EnvironmentTable } from './EnvironmentTable'
import { createMockEnvironment } from '../test/mocks/factories'
import { BrowserRouter } from 'react-router-dom'

describe('EnvironmentTable', () => {
  const env = createMockEnvironment()

  it('Renders table headers', () => {
    render(
      <BrowserRouter>
        <EnvironmentTable environments={[]} isLoading={false} />
      </BrowserRouter>
    )
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
  })

  it('Renders environment rows with correct data', () => {
    render(
      <BrowserRouter>
        <EnvironmentTable environments={[env]} isLoading={false} />
      </BrowserRouter>
    )
    expect(screen.getByText('test-env')).toBeInTheDocument()
  })
})
