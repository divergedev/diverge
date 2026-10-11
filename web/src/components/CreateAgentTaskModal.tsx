import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useCreateAgentTask } from '@/api/queries'

export function CreateAgentTaskModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('default')
  const [objective, setObjective] = useState('')
  const [repoUrl, setRepoUrl] = useState('')
  const [baseBranch, setBaseBranch] = useState('main')
  const [budgetUsd, setBudgetUsd] = useState('5.00')
  const [maxIterations, setMaxIterations] = useState('20')
  const [draftPr, setDraftPr] = useState(true)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const create = useCreateAgentTask()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)) {
      setError('Name must be lowercase alphanumeric with hyphens')
      return
    }
    if (!objective.trim()) {
      setError('Objective is required')
      return
    }
    if (!repoUrl.trim()) {
      setError('Repository URL is required')
      return
    }

    const parsedIterations = parseInt(maxIterations, 10)

    try {
      await create.mutateAsync({
        name,
        namespace: namespace || 'default',
        spec: {
          objective: objective.trim(),
          repository: {
            url: repoUrl.trim(),
            baseBranch: baseBranch.trim() || 'main',
          },
          budgetUsd: budgetUsd.trim() || '5.00',
          maxIterations: Number.isNaN(parsedIterations) ? 20 : parsedIterations,
          draftPr,
        },
      })
      onClose()
      navigate(`/tasks/${namespace || 'default'}/${name}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create agent task')
    }
  }

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>Create Agent Task</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="task-name" className="text-sm font-medium mb-1 block">Name</label>
              <Input id="task-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="fix-flaky-test" required />
            </div>
            <div>
              <label htmlFor="task-namespace" className="text-sm font-medium mb-1 block">Namespace</label>
              <Input id="task-namespace" value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="default" />
            </div>
          </div>
          <div>
            <label htmlFor="task-objective" className="text-sm font-medium mb-1 block">Objective</label>
            <Input
              id="task-objective"
              value={objective}
              onChange={(e) => setObjective(e.target.value)}
              placeholder="Fix the race condition in the checkout service tests"
              required
            />
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2">
              <label htmlFor="task-repo-url" className="text-sm font-medium mb-1 block">Repository URL</label>
              <Input
                id="task-repo-url"
                value={repoUrl}
                onChange={(e) => setRepoUrl(e.target.value)}
                placeholder="https://github.com/org/repo"
                required
              />
            </div>
            <div>
              <label htmlFor="task-base-branch" className="text-sm font-medium mb-1 block">Base Branch</label>
              <Input
                id="task-base-branch"
                value={baseBranch}
                onChange={(e) => setBaseBranch(e.target.value)}
                placeholder="main"
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="task-budget-usd" className="text-sm font-medium mb-1 block">Budget (USD)</label>
              <Input
                id="task-budget-usd"
                value={budgetUsd}
                onChange={(e) => setBudgetUsd(e.target.value)}
                placeholder="5.00"
              />
            </div>
            <div>
              <label htmlFor="task-max-iterations" className="text-sm font-medium mb-1 block">Max Iterations</label>
              <Input
                id="task-max-iterations"
                type="number"
                min={1}
                value={maxIterations}
                onChange={(e) => setMaxIterations(e.target.value)}
                placeholder="20"
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              id="task-draft-pr"
              type="checkbox"
              checked={draftPr}
              onChange={(e) => setDraftPr(e.target.checked)}
              className="h-4 w-4 rounded border-input"
            />
            <label htmlFor="task-draft-pr" className="text-sm">Open pull request as draft</label>
          </div>
          {error && <p className="text-sm text-destructive" role="alert">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={create.isPending}>{create.isPending ? 'Creating...' : 'Create Task'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
