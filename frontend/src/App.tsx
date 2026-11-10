import { useMemo } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Layout, Menu, Spin, Tag, Tooltip } from 'antd'
import {
  ApiOutlined, AppstoreOutlined, BugOutlined, CloudServerOutlined, DashboardOutlined,
  FileSearchOutlined, ExperimentOutlined, HistoryOutlined, LogoutOutlined, SettingOutlined,
  ThunderboltOutlined, DollarOutlined, DiffOutlined,
} from '@ant-design/icons'
import { api } from './api'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Targets from './pages/Targets'
import Prompts from './pages/Prompts'
import Attacks from './pages/Attacks'
import Prices from './pages/Prices'
import Settings from './pages/Settings'
import StressNew from './pages/StressNew'
import AuditNew from './pages/AuditNew'
import RequestDiffNew from './pages/RequestDiffNew'
import InjectionNew from './pages/InjectionNew'
import History from './pages/History'
import RunDetail from './pages/RunDetail'

const { Sider, Header, Content } = Layout

const menuItems = [
  { key: '/', icon: <DashboardOutlined />, label: '概览' },
  { key: '/stress/new', icon: <ThunderboltOutlined />, label: '并发压测' },
  { key: '/audit/new', icon: <FileSearchOutlined />, label: 'Token 审计' },
  { key: '/request-audit/new', icon: <DiffOutlined />, label: '请求差异审计' },
  { key: '/injection/new', icon: <BugOutlined />, label: '提示词注入' },
  { key: '/runs', icon: <HistoryOutlined />, label: '测试历史' },
  { type: 'divider' as const },
  { key: '/targets', icon: <CloudServerOutlined />, label: '上游 / 供应商' },
  { key: '/prompts', icon: <AppstoreOutlined />, label: '提示词库' },
  { key: '/attacks', icon: <ExperimentOutlined />, label: '攻击用例' },
  { key: '/prices', icon: <DollarOutlined />, label: '模型定价' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
]

function Protected({ children }: { children: React.ReactNode }) {
  const { data, isLoading } = useQuery({ queryKey: ['authStatus'], queryFn: api.authStatus })
  if (isLoading) {
    return <div style={{ height: '100vh', display: 'grid', placeItems: 'center' }}><Spin size="large" /></div>
  }
  if (data?.authEnabled && !data.authenticated) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

function Shell({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data: meta } = useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: 60000 })
  const { data: auth } = useQuery({ queryKey: ['authStatus'], queryFn: api.authStatus })

  const selected = useMemo(() => {
    const p = location.pathname
    if (p === '/') return ['/']
    const exact = menuItems.find(i => 'key' in i && i.key === p)
    if (exact) return [exact.key as string]
    if (p.startsWith('/runs')) return ['/runs']
    if (p.startsWith('/audit')) return ['/audit/new']
    if (p.startsWith('/request-audit')) return ['/request-audit/new']
    if (p.startsWith('/injection')) return ['/injection/new']
    if (p.startsWith('/stress')) return ['/stress/new']
    return []
  }, [location.pathname])

  const logout = async () => {
    await api.logout()
    await qc.invalidateQueries({ queryKey: ['authStatus'] })
    navigate('/login')
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider breakpoint="lg" collapsedWidth={0} width={218}>
        <div className="app-brand">
          <div className="logo">AU</div>
          <div>
            <div className="name">API Unit</div>
            <div className="sub">LLM 接口测试台</div>
          </div>
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={selected}
          items={menuItems as any}
          onClick={({ key }) => navigate(key)}
        />
        <div className="sider-foot">
          v{meta?.version || '1.0.0'} · 独立运行<br />
          不修改 New API 源码
        </div>
      </Sider>
      <Layout>
        <Header className="app-header">
          <div className="left">
            <ApiOutlined />
            <span>上游对接：New API / OpenAI 兼容接口</span>
          </div>
          <div className="right">
            {auth?.authEnabled ? (
              <Tag color="green">已启用访问密码</Tag>
            ) : (
              <Tag>本地模式</Tag>
            )}
            {auth?.authEnabled && (
              <Tooltip title="退出登录">
                <Button size="small" icon={<LogoutOutlined />} onClick={logout}>退出</Button>
              </Tooltip>
            )}
          </div>
        </Header>
        <Content className="app-content">{children}</Content>
      </Layout>
    </Layout>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/*"
        element={
          <Protected>
            <Shell>
              <Routes>
                <Route path="/" element={<Dashboard />} />
                <Route path="/stress/new" element={<StressNew />} />
                <Route path="/audit/new" element={<AuditNew />} />
                <Route path="/request-audit/new" element={<RequestDiffNew />} />
                <Route path="/injection/new" element={<InjectionNew />} />
                <Route path="/runs" element={<History />} />
                <Route path="/runs/:id" element={<RunDetail />} />
                <Route path="/targets" element={<Targets />} />
                <Route path="/prompts" element={<Prompts />} />
                <Route path="/attacks" element={<Attacks />} />
                <Route path="/prices" element={<Prices />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </Shell>
          </Protected>
        }
      />
    </Routes>
  )
}
