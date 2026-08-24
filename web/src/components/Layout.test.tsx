import { render, screen, waitFor } from '../test/utils'
import { Layout } from './Layout'
import { BrowserRouter } from 'react-router-dom'
import userEvent from '@testing-library/user-event'

describe('Layout', () => {
  it('Renders sidebar with nav links', () => {
    render(<BrowserRouter><Layout /></BrowserRouter>)
    expect(screen.getByText('Environments')).toBeInTheDocument()
    expect(screen.getByText('Preview Groups')).toBeInTheDocument()
    expect(screen.getByText('Cluster')).toBeInTheDocument()
  })

  it('Nav links have correct hrefs', () => {
    render(<BrowserRouter><Layout /></BrowserRouter>)
    expect(screen.getByRole('link', { name: /Environments/i })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: /Preview Groups/i })).toHaveAttribute('href', '/preview-groups')
    expect(screen.getByRole('link', { name: /Cluster/i })).toHaveAttribute('href', '/cluster')
  })

  it('Theme toggle button works', async () => {
    const user = userEvent.setup()
    render(<BrowserRouter><Layout /></BrowserRouter>)

    // Default is dark mode -> Should show "Light mode"
    const toggle = screen.getByRole('button', { name: /Light mode/i })
    expect(toggle).toBeInTheDocument()

    await user.click(toggle)
    expect(screen.getByRole('button', { name: /Dark mode/i })).toBeInTheDocument()
  })

  it('Logout button calls logout', async () => {
    const user = userEvent.setup()
    render(<BrowserRouter><Layout /></BrowserRouter>)
    const logoutBtn = screen.getByRole('button', { name: /Logout/i })
    await user.click(logoutBtn)

    // AuthProvider should handle logout, wait for redirection or token clear
    expect(localStorage.getItem('diverge:token')).toBeNull()
  })
})
