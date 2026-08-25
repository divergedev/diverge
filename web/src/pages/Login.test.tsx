import { render, screen } from '../test/utils'
import Login from './Login'

describe('Login', () => {
  it('Renders token input and connect button', () => {
    render(<Login />)
    expect(screen.getByPlaceholderText('Paste your token from: kubectl create token diverge-dashboard')).toBeInTheDocument()
    expect(screen.getByText('Connect with Token')).toBeInTheDocument()
  })

  it('Shows disabled SSO button when OIDC is not configured', () => {
    render(<Login />)
    expect(screen.getByText('SSO Login (Not Configured)')).toBeInTheDocument()
    expect(screen.getByText('SSO Login (Not Configured)').closest('button')).toBeDisabled()
  })

  it('Shows or divider between SSO and token sections', () => {
    render(<Login />)
    expect(screen.getByText('or use a token')).toBeInTheDocument()
  })
})
