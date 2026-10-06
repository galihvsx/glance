import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AuthProvider } from "./lib/auth";
import ProtectedRoute from "./components/ProtectedRoute";
import Login from "./pages/Login";
import Workspaces from "./pages/Workspaces";
import Projects from "./pages/Projects";
import ProjectOverview from "./pages/ProjectOverview";

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route element={<ProtectedRoute />}>
            <Route path="/" element={<Workspaces />} />
            <Route path="/w/:slug" element={<Projects />} />
            <Route
              path="/w/:slug/p/:identifier"
              element={<ProjectOverview />}
            />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
