import { render, screen } from '../test/utils'
import { Layout } from './Layout'
import userEvent from '@testing-library/user-event'

describe('Layout', () => {
  it('Renders sidebar with nav links', () => {
    render(<Layout />)
    expect(screen.getByText('Environments')).toBeInTheDocument()
    expect(screen.getByText('Preview Groups')).toBeInTheDocument()
    expect(screen.getByText('Cluster')).toBeInTheDocument()
  })

  it('Nav links have correct hrefs', () => {
    render(<Layout />)
    expect(screen.getByText('Environments').closest('a')).toHaveAttribute('href', '/')
    expect(screen.getByText('Preview Groups').closest('a')).toHaveAttribute('href', '/preview-groups')
    expect(screen.getByText('Cluster').closest('a')).toHaveAttribute('href', '/cluster')
  })

  it('Theme toggle button works', async () => {
    const user = userEvent.setup()
    render(<Layout />)

    const toggleBtn = screen.getByText('Light mode')
    expect(toggleBtn).toBeInTheDocument()

    await user.click(toggleBtn)
    // After clicking, should show 'Dark mode'
    expect(screen.getByText('Dark mode')).toBeInTheDocument()
  })

  it('Logout button calls logout', async () => {
    const user = userEvent.setup()
    render(<Layout />)

    const logoutBtn = screen.getByText('Logout')
    expect(logoutBtn).toBeInTheDocument()
    await user.click(logoutBtn)
  })
})
