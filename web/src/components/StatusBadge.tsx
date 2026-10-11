import { cn } from '@/lib/utils'
import { AgentTaskPhase } from '@/api/gen/diverge/v1alpha1/agenttask_pb'

const phaseStyles: Record<string, string> = {
  Ready: 'bg-green-500/20 text-green-400 border-green-500/30',
  Running: 'bg-green-500/20 text-green-400 border-green-500/30',
  Active: 'bg-green-500/20 text-green-400 border-green-500/30',
  Completed: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30',
  Provisioning: 'bg-blue-500/20 text-blue-400 border-blue-500/30 animate-pulse',
  Pending: 'bg-blue-500/20 text-blue-400 border-blue-500/30 animate-pulse',
  Migrating: 'bg-amber-500/20 text-amber-400 border-amber-500/30 animate-pulse',
  Pausing: 'bg-amber-500/20 text-amber-400 border-amber-500/30 animate-pulse',
  Paused: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
  Deploying: 'bg-blue-500/20 text-blue-400 border-blue-500/30 animate-pulse',
  Error: 'bg-red-500/20 text-red-400 border-red-500/30',
  Failed: 'bg-red-500/20 text-red-400 border-red-500/30',
  Terminating: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
  Deleting: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
}

export function formatAgentTaskPhase(phase?: AgentTaskPhase): string {
  switch (phase) {
    case AgentTaskPhase.PENDING: return 'Pending'
    case AgentTaskPhase.PROVISIONING: return 'Provisioning'
    case AgentTaskPhase.ACTIVE: return 'Active'
    case AgentTaskPhase.PAUSING: return 'Pausing'
    case AgentTaskPhase.PAUSED: return 'Paused'
    case AgentTaskPhase.COMPLETED: return 'Completed'
    case AgentTaskPhase.FAILED: return 'Failed'
    default: return 'Unknown'
  }
}

export function StatusBadge({ phase, className }: { phase: string; className?: string }) {
  const isKnown = Object.prototype.hasOwnProperty.call(phaseStyles, phase)
  const style = isKnown ? phaseStyles[phase] : 'bg-gray-500/20 text-gray-400 border-gray-500/30'
  return (
    <span className={cn('inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold', style, className)}>
      {phase || 'Unknown'}
    </span>
  )
}
