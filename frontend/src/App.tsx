import { Route, Routes } from 'react-router-dom'
import AuthGate from './components/AuthGate'
import Layout from './components/Layout'
import Accounts from './pages/Accounts'
import CPASync from './pages/CPASync'
import Dashboard from './pages/Dashboard'
import Operations from './pages/Operations'
import Proxies from './pages/Proxies'
import Settings from './pages/Settings'

export default function App() {
  return (
    <AuthGate>
      <Layout>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/cpa-sync" element={<CPASync />} />
          <Route path="/proxies" element={<Proxies />} />
          <Route path="/ops" element={<Operations />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </Layout>
    </AuthGate>
  )
}
