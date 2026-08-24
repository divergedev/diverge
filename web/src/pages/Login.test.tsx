import { render, screen } from '../test/utils'
import Login from './Login'
import { BrowserRouter } from 'react-router-dom'

describe('Login', () => {
  it('Renders token input and connect button', () => {
    render(
      <BrowserRouter><Login /></BrowserRouter>
    )
    expect(screen.getByPlaceholderText(/Paste your token/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Connect/i })).toBeInTheDocument()
  })
})
