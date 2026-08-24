import { Routes, Route } from 'react-router-dom'
import { ProtectedRoute } from '@/hooks/useAuth'
import { Layout } from '@/components/Layout'
import Login from '@/pages/Login'
import EnvironmentList from '@/pages/EnvironmentList'
import EnvironmentDetail from '@/pages/EnvironmentDetail'
import PreviewGroups from '@/pages/PreviewGroups'
import PreviewGroupDetail from '@/pages/PreviewGroupDetail'
import ClusterInfo from '@/pages/ClusterInfo'

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route element={<ProtectedRoute><Layout /></ProtectedRoute>}>
        <Route index element={<EnvironmentList />} />
        <Route path="environments/:namespace/:name" element={<EnvironmentDetail />} />
        <Route path="preview-groups" element={<PreviewGroups />} />
        <Route path="preview-groups/:namespace/:name" element={<PreviewGroupDetail />} />
        <Route path="cluster" element={<ClusterInfo />} />
      </Route>
    </Routes>
  )
}
