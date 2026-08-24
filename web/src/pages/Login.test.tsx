import { render, screen } from '../test/utils'
import Login from './Login'

describe('Login', () => {
  it('Renders token input and connect button', () => {
    render(<Login />)
    expect(screen.getByPlaceholderText('Paste your token from: kubectl create token diverge-dashboard')).toBeInTheDocument()
    expect(screen.getByText('Connect')).toBeInTheDocument()
  })
})
