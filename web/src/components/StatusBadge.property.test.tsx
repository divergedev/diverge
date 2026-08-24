import { render, cleanup } from '../test/utils'
import { StatusBadge } from './StatusBadge'
import fc from 'fast-check'

describe('StatusBadge Properties', () => {
  afterEach(cleanup)

  it('any string phase renders without throwing', () => {
    fc.assert(
      fc.property(fc.string(), (phase) => {
        render(<StatusBadge phase={phase} />)
        cleanup()
      })
    )
  })

  it('unknown phases always produce gray CSS classes', () => {
    const knownPhases = ['Ready', 'Running', 'Provisioning', 'Pending', 'Error', 'Failed', 'Terminating', 'Deleting']
    fc.assert(
      fc.property(
        fc.string().filter(s => !knownPhases.includes(s) && s.length > 0),
        (phase) => {
          const { container } = render(<StatusBadge phase={phase} />)
          expect(container.querySelector('span')).toHaveClass('bg-gray-500/20')
          cleanup()
        }
      )
    )
  })
})
