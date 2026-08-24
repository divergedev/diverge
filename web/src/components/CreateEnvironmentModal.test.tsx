import { render, screen, waitFor } from '../test/utils'
import { CreateEnvironmentModal } from './CreateEnvironmentModal'
import userEvent from '@testing-library/user-event'

describe('CreateEnvironmentModal', () => {
  it('Renders form fields', () => {
    render(<CreateEnvironmentModal open={true} onClose={() => {}} />)
    expect(screen.getByPlaceholderText('my-feature')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('default')).toBeInTheDocument()
    expect(screen.getByDisplayValue('24 hours')).toBeInTheDocument() // TTL Select
  })

  it('Validates name format (lowercase alphanumeric)', async () => {
    const user = userEvent.setup()
    render(<CreateEnvironmentModal open={true} onClose={() => {}} />)

    const nameInput = screen.getByPlaceholderText('my-feature')
    await user.type(nameInput, 'Invalid_Name')

    await user.click(screen.getByText('Create'))
    expect(screen.getByText('Name must be lowercase alphanumeric with hyphens')).toBeInTheDocument()
  })

  it('Cancel closes modal', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<CreateEnvironmentModal open={true} onClose={onClose} />)

    await user.click(screen.getByText('Cancel'))
    expect(onClose).toHaveBeenCalled()
  })

  it('Submit calls createEnvironment mutation', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<CreateEnvironmentModal open={true} onClose={onClose} />)

    const nameInput = screen.getByPlaceholderText('my-feature')
    await user.type(nameInput, 'valid-name')

    await user.click(screen.getByText('Create'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    }, { timeout: 10000 })
  }, 15000)
})
