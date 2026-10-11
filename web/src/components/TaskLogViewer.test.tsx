import { render, screen, waitFor, fireEvent } from '@/test/utils'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { Timestamp } from '@bufbuild/protobuf'
import { TaskLogViewer } from './TaskLogViewer'
import { agentTaskClient } from '@/api/client'

vi.mock('@/api/client', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/api/client')>()
  return {
    ...mod,
    agentTaskClient: {
      ...mod.agentTaskClient,
      streamTaskLogs: vi.fn(),
    },
  }
})

const mockStreamTaskLogs = vi.mocked(agentTaskClient.streamTaskLogs)

function encodeChunk(text: string): Uint8Array {
  return new TextEncoder().encode(text)
}

describe('TaskLogViewer', () => {
  beforeEach(() => {
    mockStreamTaskLogs.mockReset()
  })

  it('buffers partial log chunks across messages and flushes remaining text at stream end', async () => {
    const ts = Timestamp.fromDate(new Date('2026-10-10T12:00:00Z'))
    mockStreamTaskLogs.mockImplementation(async function* () {
      yield { chunk: encodeChunk('Starting agent '), timestamp: ts } as never
      yield { chunk: encodeChunk('sandbox...\nCloning repo\nPartial final line'), timestamp: ts } as never
    })

    render(<TaskLogViewer namespace="default" name="task-1" />)

    expect(await screen.findByText('Starting agent sandbox...')).toBeInTheDocument()
    expect(screen.getByText('Cloning repo')).toBeInTheDocument()
    expect(screen.getByText('Partial final line')).toBeInTheDocument()
    expect(screen.getByText('3 lines')).toBeInTheDocument()
  })

  it('clears previous task logs when namespace or name changes', async () => {
    const ts = Timestamp.fromDate(new Date('2026-10-10T12:00:00Z'))
    mockStreamTaskLogs.mockImplementation(async function* (req: { name?: string }) {
      yield { chunk: encodeChunk(`Log from ${req.name}\n`), timestamp: ts } as never
    })

    const { rerender } = render(<TaskLogViewer namespace="default" name="task-alpha" />)
    expect(await screen.findByText('Log from task-alpha')).toBeInTheDocument()

    rerender(<TaskLogViewer namespace="default" name="task-beta" />)
    expect(await screen.findByText('Log from task-beta')).toBeInTheDocument()
    expect(screen.queryByText('Log from task-alpha')).not.toBeInTheDocument()
  })

  it('displays stream error banner and retries streaming when Retry is clicked', async () => {
    let callCount = 0
    const ts = Timestamp.fromDate(new Date('2026-10-10T12:00:00Z'))
    mockStreamTaskLogs.mockImplementation(async function* () {
      callCount++
      if (callCount === 1) {
        throw new Error('sandbox pod not yet bound')
      }
      yield { chunk: encodeChunk('Recovered log stream\n'), timestamp: ts } as never
    })

    render(<TaskLogViewer namespace="default" name="task-retry" />)

    expect(await screen.findByRole('alert')).toHaveTextContent('sandbox pod not yet bound')
    expect(screen.getByText('Log stream disconnected.')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))

    expect(await screen.findByText('Recovered log stream')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })
  })

  it('toggles timestamps and clears log lines', async () => {
    const ts = Timestamp.fromDate(new Date('2026-10-10T12:00:00.000Z'))
    mockStreamTaskLogs.mockImplementation(async function* () {
      yield { chunk: encodeChunk('Step 1 complete\n'), timestamp: ts } as never
    })

    render(<TaskLogViewer namespace="default" name="task-controls" />)

    expect(await screen.findByText('Step 1 complete')).toBeInTheDocument()
    expect(screen.getByText('2026-10-10T12:00:00.000Z')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Timestamps/i }))
    expect(screen.queryByText('2026-10-10T12:00:00.000Z')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Clear/i }))
    expect(screen.getByText('Waiting for sandbox logs...')).toBeInTheDocument()
  })
})
