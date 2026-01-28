import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Code2,
  PlayCircle,
  ScrollText,
  BarChart3,
  Settings,
  Zap,
  Layers,
  Globe,
  GitBranch,
  AlertTriangle,
  FileText,
  Gauge,
  Bell,
  Network,
  Flame,
} from 'lucide-react'
import { cn } from '../../utils/format'

const navItems = [
  { to: '/dashboard', icon: LayoutDashboard, label: '概览' },
  { to: '/functions', icon: Code2, label: '函数' },
  { to: '/workflows', icon: GitBranch, label: '工作流' },
  { to: '/dependencies', icon: Network, label: '依赖分析' },
  { to: '/invocations', icon: PlayCircle, label: '调用' },
  { to: '/dlq', icon: AlertTriangle, label: '死信队列' },
  { to: '/layers', icon: Layers, label: '层' },
  { to: '/environments', icon: Globe, label: '环境' },
  { to: '/warming', icon: Flame, label: '预热' },
  { to: '/logs', icon: ScrollText, label: '日志' },
  { to: '/metrics', icon: BarChart3, label: '监控' },
  { to: '/alerts', icon: Bell, label: '告警' },
  { to: '/quota', icon: Gauge, label: '配额' },
  { to: '/audit', icon: FileText, label: '审计' },
  { to: '/settings', icon: Settings, label: '设置' },
]

interface SidebarProps {
  collapsed: boolean
  onToggle: () => void
}

export default function Sidebar({ collapsed, onToggle }: SidebarProps) {
  return (
    <aside
      className={cn(
        'bg-sidebar text-sidebar-foreground flex flex-col border-r border-sidebar-border transition-all duration-300 ease-in-out',
        collapsed ? 'w-16' : 'w-64'
      )}
    >
      {/* Logo - 点击切换折叠 */}
      <div
        onClick={onToggle}
        className="h-16 flex items-center px-3 border-b border-sidebar-border cursor-pointer group transition-colors"
        title={collapsed ? '展开侧边栏' : '收起侧边栏'}
      >
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-gradient-to-br from-accent/20 to-accent/5 flex-shrink-0 transition-all duration-300 group-hover:shadow-glow-accent group-hover:scale-105">
          <Zap className="w-5 h-5 text-accent transition-transform duration-300 group-hover:rotate-12" />
        </div>
        {!collapsed && (
          <span className="ml-3 text-xl font-display font-semibold text-accent whitespace-nowrap overflow-hidden">
            Nimbus
          </span>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 py-4">
        <ul className="space-y-1 px-2">
          {navItems.map((item, index) => (
            <li key={item.to} style={{ animationDelay: `${index * 50}ms` }} className="animate-fade-in-up">
              <NavLink
                to={item.to}
                title={collapsed ? item.label : undefined}
                className={({ isActive }) =>
                  cn(
                    'flex items-center rounded-lg transition-all duration-200 group',
                    collapsed ? 'justify-center px-2 py-3' : 'px-4 py-3',
                    isActive
                      ? 'bg-accent text-accent-foreground font-medium shadow-glow'
                      : 'text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground'
                  )
                }
              >
                <item.icon className="w-5 h-5 flex-shrink-0 transition-transform duration-200 group-hover:scale-110" />
                {!collapsed && <span className="ml-3 whitespace-nowrap">{item.label}</span>}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      {/* Footer */}
      <div className="p-3 border-t border-sidebar-border">
        <div className={cn('flex items-center gap-3', collapsed ? 'justify-center' : 'px-2')}>
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-accent/30 to-accent/10 flex-shrink-0">
            <span className="text-xs font-display font-semibold text-accent">N</span>
          </div>
          {!collapsed && (
            <div className="flex-1 min-w-0">
              <p className="text-sm font-display font-medium text-foreground truncate">Nimbus</p>
              <p className="text-xs text-muted-foreground font-mono">v0.1.0</p>
            </div>
          )}
        </div>
      </div>
    </aside>
  )
}
