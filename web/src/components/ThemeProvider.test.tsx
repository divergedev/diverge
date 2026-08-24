import { render, screen } from '../test/utils'
import { ThemeProvider, useTheme } from './ThemeProvider'
import userEvent from '@testing-library/user-event'

const TestComponent = () => {
  const { theme, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme-val">{theme}</span>
      <button onClick={() => setTheme('light')}>Toggle</button>
    </div>
  )
}

describe('ThemeProvider', () => {
  beforeEach(() => localStorage.clear())

  it('Defaults to dark theme', () => {
    render(
      <ThemeProvider defaultTheme="dark">
        <TestComponent />
      </ThemeProvider>
    )
    expect(screen.getByTestId('theme-val')).toHaveTextContent('dark')
  })

  it('Toggles to light theme', async () => {
    const user = userEvent.setup()
    render(
      <ThemeProvider defaultTheme="dark">
        <TestComponent />
      </ThemeProvider>
    )
    await user.click(screen.getByText('Toggle'))
    expect(screen.getByTestId('theme-val')).toHaveTextContent('light')
  })
})
