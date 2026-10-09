import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AuthProvider } from "./lib/auth";
import { QueryProvider } from "./lib/query";
import ProtectedRoute from "./components/ProtectedRoute";
import ProjectScope from "./components/ProjectScope";
import Login from "./pages/Login";
import Onboarding from "./pages/Onboarding";
import Home from "./pages/Home";
import Workspaces from "./pages/Workspaces";
import WorkspaceSettings from "./pages/WorkspaceSettings";
import Projects from "./pages/Projects";
import Issues from "./pages/Issues";
import IssueDetail from "./pages/IssueDetail";
import Intake from "./pages/Intake";
import Board from "./pages/Board";
import Calendar from "./pages/Calendar";
import Gantt from "./pages/Gantt";
import Cycles from "./pages/Cycles";
import Modules from "./pages/Modules";
import Pages from "./pages/Pages";
import Spreadsheet from "./pages/Spreadsheet";
import { Toaster } from "./components/ui/toast";

export default function App() {
  return (
    <AuthProvider>
      <QueryProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route element={<ProtectedRoute />}>
              <Route path="/onboarding" element={<Onboarding />} />
              <Route path="/" element={<Home />} />
              <Route path="/w" element={<Workspaces />} />
              <Route path="/w/:slug" element={<Projects />} />
              <Route path="/w/:slug/settings" element={<WorkspaceSettings />} />
              <Route element={<ProjectScope />}>
                <Route path="/w/:slug/p/:identifier" element={<Issues />} />
                <Route
                  path="/w/:slug/p/:identifier/i/:uuid"
                  element={<IssueDetail />}
                />
                <Route
                  path="/w/:slug/p/:identifier/intake"
                  element={<Intake />}
                />
                <Route
                  path="/w/:slug/p/:identifier/board"
                  element={<Board />}
                />
                <Route
                  path="/w/:slug/p/:identifier/spreadsheet"
                  element={<Spreadsheet />}
                />
                <Route
                  path="/w/:slug/p/:identifier/calendar"
                  element={<Calendar />}
                />
                <Route
                  path="/w/:slug/p/:identifier/gantt"
                  element={<Gantt />}
                />
                <Route
                  path="/w/:slug/p/:identifier/cycles"
                  element={<Cycles />}
                />
                <Route
                  path="/w/:slug/p/:identifier/modules"
                  element={<Modules />}
                />
                <Route
                  path="/w/:slug/p/:identifier/pages"
                  element={<Pages />}
                />
              </Route>
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
          <Toaster />
        </BrowserRouter>
      </QueryProvider>
    </AuthProvider>
  );
}
