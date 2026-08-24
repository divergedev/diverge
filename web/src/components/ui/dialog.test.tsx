import { render, screen } from '../../test/utils'
import { Dialog, DialogContent, DialogTitle } from './dialog'

describe('Dialog', () => {
  it('Opens when open=true', () => {
    render(
      <Dialog open={true} onClose={() => {}}>
        <DialogContent>
          <DialogTitle>My Dialog</DialogTitle>
          <p>Content</p>
        </DialogContent>
      </Dialog>
    )
    expect(screen.getByText('My Dialog')).toBeInTheDocument()
    expect(screen.getByText('Content')).toBeInTheDocument()
  })
})
