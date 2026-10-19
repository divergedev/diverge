import { useState, useEffect } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { stringify as yamlStringify } from 'yaml'
import {
  useGetAgentTask,
  useDeleteAgentTask,
  usePauseAgentTask,
  useResumeAgentTask,
  useGuideAgentTask,
  useListEnvironments,
} from '@/api/queries'
import { AgentTaskPhase } from '@/api/gen/diverge/v1alpha1/agenttask_pb'
import { StatusBadge, formatAgentTaskPhase } from '@/components/StatusBadge'
import { TaskLogViewer } from '@/components/TaskLogViewer'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { isSafeExternalUrl } from '@/lib/utils'
import {
  ArrowLeft,
  ExternalLink,
  Trash2,
  Pause,
  Play,
  Send,
  AlertCircle,
  CheckCircle,
} from 'lucide-react'

/**
 * Detail view for an individual AgentTask with live steering, pause/resume, log streaming, and YAML inspection.
 */
export default function AgentTaskDetail() {
  const { namespace = '', name = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading, error, refetch } = useGetAgentTask(namespace, name)
  const { data: envListData } = useListEnvironments({ namespace })
  const deleteTask = useDeleteAgentTask()
  const pauseTask = usePauseAgentTask()
  const resumeTask = useResumeAgentTask()
  const guideTask = useGuideAgentTask()

  const [activeTab, setActiveTab] = useState('overview')
  const [mutationError, setMutationError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [guidanceMessage, setGuidanceMessage] = useState('')
  const [resumeDialogOpen, setResumeDialogOpen] = useState(false)
  const [budgetUsdBump, setBudgetUsdBump] = useState('')
  const [resumeGuidance, setResumeGuidance] = useState('')

  useEffect(() => {
    if (!successMessage) return
    const timer = setTimeout(() => setSuccessMessage(null), 4000)
    return () => clearTimeout(timer)
  }, [successMessage])

  if (isLoading) {
    return <div className="flex justify-center py-16"><div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" /></div>
  }

  if (error) {
    return (
      <div className="text-center py-16 space-y-4" role="alert">
        <AlertCircle className="h-10 w-10 text-destructive mx-auto" />
        <h2 className="text-xl font-semibold">Failed to load agent task</h2>
        <p className="text-muted-foreground">{error.message}</p>
        <Button variant="outline" onClick={() => refetch()}>Retry</Button>
      </div>
    )
  }

  const task = data?.task
  if (!task) {
    return <div className="text-center py-16"><h2 className="text-xl font-semibold">Agent task not found</h2></div>
  }

  const phase = task.status?.phase
  const phaseLabel = formatAgentTaskPhase(phase)
  const isPaused = phase === AgentTaskPhase.PAUSED || task.spec?.suspended
  const isTerminal = phase === AgentTaskPhase.COMPLETED || phase === AgentTaskPhase.FAILED
  const canPause = !isPaused && (phase === AgentTaskPhase.ACTIVE || phase === AgentTaskPhase.PROVISIONING || phase === AgentTaskPhase.PENDING)
  const canGuide = !isTerminal
  const safePrUrl = isSafeExternalUrl(task.status?.prUrl) ? task.status?.prUrl : undefined
  const linkedEnv = (envListData?.environments ?? []).find(
    (env) =>
      env.labels?.['divergedev.com/agent-task'] === name ||
      env.name === `agent-preview-${name}`,
  )
  const safePreviewUrl = isSafeExternalUrl(linkedEnv?.status?.url)
    ? linkedEnv?.status?.url
    : undefined

  const handleDelete = async () => {
    if (!confirm(`Delete agent task ${namespace}/${name}?`)) return
    setMutationError(null)
    try {
      await deleteTask.mutateAsync({ namespace, name })
      navigate('/tasks')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to delete task')
    }
  }

  const handlePause = async () => {
    setMutationError(null)
    try {
      await pauseTask.mutateAsync({ namespace, name })
      setSuccessMessage('Task paused')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to pause task')
    }
  }

  const handleResume = async () => {
    setMutationError(null)
    const trimmedBump = budgetUsdBump.trim().replace(/^[+$]+/, '').trim()
    if (trimmedBump) {
      const parsedBump = Number(trimmedBump)
      if (!Number.isFinite(parsedBump) || parsedBump <= 0) {
        setMutationError('Additional budget USD must be a positive number')
        setResumeDialogOpen(false)
        return
      }
    }
    try {
      if (resumeGuidance.trim()) {
        await guideTask.mutateAsync({
          namespace,
          name,
          message: resumeGuidance.trim(),
        })
      }
      await resumeTask.mutateAsync({
        namespace,
        name,
        budgetUsdBump: trimmedBump || undefined,
      })
      setResumeDialogOpen(false)
      setBudgetUsdBump('')
      setResumeGuidance('')
      setSuccessMessage(trimmedBump ? `Task resumed (+$${trimmedBump} budget)` : 'Task resumed')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to resume task')
      setResumeDialogOpen(false)
    }
  }

  const handleGuide = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!guidanceMessage.trim() || !canGuide) return
    setMutationError(null)
    try {
      await guideTask.mutateAsync({
        namespace,
        name,
        message: guidanceMessage.trim(),
      })
      setGuidanceMessage('')
      setSuccessMessage('Guidance sent to agent sandbox')
    } catch (err) {
      setMutationError(err instanceof Error ? err.message : 'Failed to send guidance')
    }
  }

  return (
    <div className="space-y-6">
      {mutationError && (
        <div className="bg-destructive/10 border border-destructive/20 text-destructive px-4 py-3 rounded-md flex items-center gap-2 text-sm" role="alert">
          <AlertCircle className="h-4 w-4 flex-shrink-0" />
          {mutationError}
        </div>
      )}

      {successMessage && (
        <div className="bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-emerald-400 px-4 py-3 rounded-md flex items-center gap-2 text-sm" role="status">
          <CheckCircle className="h-4 w-4 flex-shrink-0" />
          {successMessage}
        </div>
      )}

      <div className="flex items-center gap-4">
        <Link to="/tasks" aria-label="Back to agent tasks" className="inline-flex items-center justify-center h-9 w-9 rounded-md text-sm font-medium hover:bg-accent hover:text-accent-foreground">
          <ArrowLeft className="h-4 w-4" />
        </Link>
        <div className="flex-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold">{task.name}</h1>
            <StatusBadge phase={phaseLabel} />
          </div>
          <p className="text-muted-foreground">{task.namespace}</p>
        </div>
        <div className="flex items-center gap-2">
          {safePreviewUrl && (
            <a href={safePreviewUrl} target="_blank" rel="noopener noreferrer">
              <Button variant="outline" size="sm"><ExternalLink className="h-4 w-4 mr-2" />Open Preview</Button>
            </a>
          )}
          {safePrUrl && (
            <a href={safePrUrl} target="_blank" rel="noopener noreferrer">
              <Button variant="outline" size="sm"><ExternalLink className="h-4 w-4 mr-2" />Open PR</Button>
            </a>
          )}
          {canPause && (
            <Button variant="outline" size="sm" onClick={handlePause} disabled={pauseTask.isPending}>
              <Pause className="h-4 w-4 mr-2" />Pause
            </Button>
          )}
          {isPaused && (
            <Button variant="outline" size="sm" onClick={() => setResumeDialogOpen(true)} disabled={resumeTask.isPending}>
              <Play className="h-4 w-4 mr-2" />Resume
            </Button>
          )}
          <Button variant="destructive" size="sm" onClick={handleDelete} disabled={deleteTask.isPending}>
            <Trash2 className="h-4 w-4 mr-2" />Delete
          </Button>
        </div>
      </div>

      {/* Live Steering Guidance Bar */}
      <Card>
        <CardContent className="p-4">
          <form onSubmit={handleGuide} className="flex items-center gap-3">
            <Input
              aria-label="Guidance message"
              placeholder={
                canGuide
                  ? 'Send live steering guidance or instructions to the agent...'
                  : isTerminal
                    ? 'Task has completed and can no longer receive live guidance'
                    : 'Task is paused; resume the task to send live guidance'
              }
              value={guidanceMessage}
              onChange={(e) => setGuidanceMessage(e.target.value)}
              disabled={!canGuide}
              className="flex-1"
            />
            <Button type="submit" size="sm" disabled={!canGuide || guideTask.isPending || !guidanceMessage.trim()}>
              <Send className="h-4 w-4 mr-2" />
              {guideTask.isPending ? 'Sending...' : 'Guide Agent'}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <Card>
              <CardHeader><CardTitle className="text-base">Objective & Repository</CardTitle></CardHeader>
              <CardContent className="space-y-3 text-sm">
                <div>
                  <span className="text-muted-foreground block mb-1">Objective</span>
                  <p className="font-medium whitespace-pre-wrap">{task.spec?.objective || '—'}</p>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Repository</span>
                  <span className="font-mono text-xs">{task.spec?.repository?.url || '—'}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Base Branch</span>
                  <span>{task.spec?.repository?.baseBranch || 'main'}</span>
                </div>
                {task.spec?.repository?.workingBranch && (
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">Working Branch</span>
                    <span className="font-mono text-xs">{task.spec.repository.workingBranch}</span>
                  </div>
                )}
                {task.status?.message && (
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">Status Message</span>
                    <span>{task.status.message}</span>
                  </div>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader><CardTitle className="text-base">Execution & Budget</CardTitle></CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Phase</span>
                  <StatusBadge phase={phaseLabel} />
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Iterations</span>
                  <span>{task.status?.iteration ?? 0} / {task.spec?.maxIterations || '∞'}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Cost (USD)</span>
                  <span>${task.status?.costUsd || '0.00'} / ${task.spec?.budgetUsd || '—'}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Tokens Consumed</span>
                  <span>{String(task.status?.tokensConsumed ?? 0)}{task.spec?.budgetTokens ? ` / ${String(task.spec.budgetTokens)}` : ''}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Sandbox Claim</span>
                  <span className="font-mono text-xs">{task.status?.sandboxClaimRef || '—'}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Sandbox Pod</span>
                  <span className="font-mono text-xs">{task.status?.sandboxPodName || '—'}</span>
                </div>
                {linkedEnv && (
                  <div className="flex justify-between items-center pt-1 border-t border-border/50">
                    <span className="text-muted-foreground">Linked Preview</span>
                    <div className="flex items-center gap-2">
                      <Link
                        to={`/environments/${linkedEnv.namespace || namespace}/${linkedEnv.name}`}
                        className="text-primary hover:underline font-mono text-xs"
                      >
                        {linkedEnv.name}
                      </Link>
                      <StatusBadge phase={linkedEnv.status?.phase || 'Pending'} />
                      {safePreviewUrl && (
                        <a
                          href={safePreviewUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
                        >
                          Open Preview
                          <ExternalLink className="h-3 w-3" />
                        </a>
                      )}
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="logs">
          <TaskLogViewer namespace={namespace} name={name} />
        </TabsContent>

        <TabsContent value="yaml">
          <Card>
            <CardContent className="p-0">
              <pre className="p-4 text-xs font-mono overflow-auto max-h-[600px] bg-black text-gray-200 rounded-md">
                {yamlStringify(task.toJson?.() ?? task)}
              </pre>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Dialog open={resumeDialogOpen} onClose={() => setResumeDialogOpen(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Resume Agent Task</DialogTitle>
            <DialogDescription>
              Resume execution of {namespace}/{name}. Optionally increase the USD budget or attach steering guidance.
            </DialogDescription>
          </DialogHeader>
          <div className="py-2 space-y-3">
            <div>
              <label htmlFor="budget-usd-bump" className="text-sm font-medium mb-1 block">Additional Budget USD (optional)</label>
              <Input
                id="budget-usd-bump"
                value={budgetUsdBump}
                onChange={(e) => setBudgetUsdBump(e.target.value)}
                placeholder="e.g. 5.00"
              />
            </div>
            <div>
              <label htmlFor="resume-guidance" className="text-sm font-medium mb-1 block">Guidance for Resume (optional)</label>
              <Input
                id="resume-guidance"
                value={resumeGuidance}
                onChange={(e) => setResumeGuidance(e.target.value)}
                placeholder="e.g. Focus on fixing the nil pointer in handler.go"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setResumeDialogOpen(false)}>Cancel</Button>
            <Button onClick={handleResume} disabled={resumeTask.isPending || guideTask.isPending}>
              {resumeTask.isPending || guideTask.isPending
                ? 'Resuming...'
                : resumeGuidance.trim()
                  ? 'Guide & Resume'
                  : 'Resume'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
