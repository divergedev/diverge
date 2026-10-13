import { render, screen, fireEvent } from '@/test/utils'
import { server } from '@/test/mocks/server'
import { http, HttpResponse } from 'msw'
import { describe, it, expect, vi } from 'vitest'
import EnvironmentList from './EnvironmentList'

describe('EnvironmentList', () => {
  it('renders FinOps summary cards, per-environment cost, hibernation badge, and magic link copy button', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })

    server.use(
      http.post('*/diverge.v1alpha1.EnvironmentService/ListEnvironments', () =>
        HttpResponse.json({
          environments: [
            {
              name: 'pr-active',
              namespace: 'default',
              spec: {
                source: { branch: 'feat/checkout' },
                database: { mode: 'schema' },
                routing: { headerValue: 'pr-active' },
              },
              status: {
                phase: 'Ready',
                url: 'https://preview.example.com',
              },
            },
            {
              name: 'pr-idle',
              namespace: 'default',
              annotations: {
                'divergedev.com/hibernated': 'true',
              },
              spec: {
                source: { branch: 'feat/idle' },
              },
              status: {
                phase: 'Ready',
                url: 'https://idle.preview.example.com',
              },
            },
          ],
        }),
      ),
    )

    render(<EnvironmentList />)

    expect(await screen.findByText('pr-active')).toBeInTheDocument()

    // Active env ($0.025 + $0.005 = $0.030/hr) + Hibernated env ($0.000/hr) = $0.030/hr
    expect(screen.getByText('Estimated Hourly Spend')).toBeInTheDocument()
    expect(screen.getAllByText('$0.030/hr').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('~$21.90/mo projected run-rate')).toBeInTheDocument()

    // Compute state summary
    expect(screen.getByText('Compute State')).toBeInTheDocument()
    expect(screen.getByText('1 Active · 1 Hibernated')).toBeInTheDocument()

    // Table headers and rows
    expect(screen.getByText('Est. Cost')).toBeInTheDocument()
    expect(screen.getByText('Hibernated')).toBeInTheDocument()
    expect(screen.getByText('$0.000/hr')).toBeInTheDocument()

    // Copy magic link button
    const magicBtn = screen.getByRole('button', { name: 'Copy magic link for pr-active' })
    fireEvent.click(magicBtn)
    expect(writeText).toHaveBeenCalledWith('https://preview.example.com/?_diverge_env=pr-active')
  })
})
