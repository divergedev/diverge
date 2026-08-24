import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { useCreateEnvironment } from '@/api/queries'

const TTL_OPTIONS = [
  { label: '1 hour', value: '3600s' },
  { label: '4 hours', value: '14400s' },
  { label: '8 hours', value: '28800s' },
  { label: '24 hours', value: '86400s' },
  { label: '48 hours', value: '172800s' },
  { label: 'No limit', value: '' },
]

export function CreateEnvironmentModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('default')
  const [branch, setBranch] = useState('')
  const [ttl, setTtl] = useState('86400s')
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const create = useCreateEnvironment()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(name) && name.length > 1) {
      setError('Name must be lowercase alphanumeric with hyphens')
      return
    }

    try {
      await create.mutateAsync({
        name,
        namespace,
        spec: {
          source: branch ? { branch } : undefined,
          lifecycle: ttl ? { ttl: { seconds: BigInt(parseInt(ttl)) } } : undefined,
        },
      })
      onClose()
      navigate(`/environments/${namespace}/${name}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create environment')
    }
  }

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogContent>
        <DialogHeader><DialogTitle>Create Environment</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="text-sm font-medium mb-1 block">Name</label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-feature" required />
          </div>
          <div>
            <label className="text-sm font-medium mb-1 block">Namespace</label>
            <Input value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="default" />
          </div>
          <div>
            <label className="text-sm font-medium mb-1 block">Branch</label>
            <Input value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="feature/my-branch (optional)" />
          </div>
          <div>
            <label className="text-sm font-medium mb-1 block">TTL</label>
            <Select value={ttl} onChange={(e) => setTtl(e.target.value)}>
              {TTL_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </Select>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={create.isPending}>{create.isPending ? 'Creating...' : 'Create'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
