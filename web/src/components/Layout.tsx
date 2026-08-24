import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { Home, GitBranch, Server, Moon, Sun, LogOut, Menu, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/hooks/useAuth'
import { useTheme } from '@/components/ThemeProvider'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', icon: Home, label: 'Environments' },
  { to: '/preview-groups', icon: GitBranch, label: 'Preview Groups' },
  { to: '/cluster', icon: Server, label: 'Cluster' },
]

export function Layout() {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const { user, logout } = useAuth()
  const { theme, setTheme } = useTheme()

  return (
    <div className="min-h-screen flex">
      {/* Mobile overlay */}
      {sidebarOpen && <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setSidebarOpen(false)} />}

      {/* Sidebar */}
      <aside className={cn(
        'fixed inset-y-0 left-0 z-50 w-60 bg-card border-r flex flex-col transition-transform lg:translate-x-0 lg:static',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      )}>
        <div className="p-4 border-b">
          <div className="flex items-center gap-2 font-bold text-lg">🔀 Diverge</div>
          <div className="text-xs text-muted-foreground mt-0.5">Dashboard</div>
        </div>

        <nav className="flex-1 p-2 space-y-1">
          {navItems.map(({ to, icon: Icon, label }) => (
            <NavLink key={to} to={to} end={to === '/'} onClick={() => setSidebarOpen(false)}
              className={({ isActive }) => cn(
                'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                isActive ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground',
              )}>
              <Icon className="h-4 w-4" />{label}
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t space-y-2">
          <Button variant="ghost" size="sm" className="w-full justify-start gap-2"
            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            {theme === 'dark' ? 'Light mode' : 'Dark mode'}
          </Button>
          {user && <div className="text-xs text-muted-foreground truncate px-2">{user.userId}</div>}
          <Button variant="ghost" size="sm" className="w-full justify-start gap-2 text-destructive" onClick={logout}>
            <LogOut className="h-4 w-4" />Logout
          </Button>
        </div>
      </aside>

      {/* Main */}
      <div className="flex-1 flex flex-col min-w-0">
        <header className="lg:hidden flex items-center gap-2 p-4 border-b">
          <Button variant="ghost" size="icon" onClick={() => setSidebarOpen(true)}><Menu className="h-5 w-5" /></Button>
          <span className="font-bold">🔀 Diverge</span>
        </header>
        <main className="flex-1 p-6 overflow-auto"><Outlet /></main>
      </div>
    </div>
  )
}
