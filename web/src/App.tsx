import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AuthProvider } from "./lib/auth";
import { QueryProvider } from "./lib/query";
import ProtectedRoute from "./components/ProtectedRoute";
import GlobalScope from "./components/GlobalScope";
import WorkspaceScope from "./components/WorkspaceScope";
import ProjectScope from "./components/ProjectScope";
import AdminGuard from "./components/AdminGuard";
import Admin from "./pages/Admin";
import Login from "./pages/Login";
import Onboarding from "./pages/Onboarding";
import Home from "./pages/Home";
import Workspaces from "./pages/Workspaces";
import WorkspaceSettings from "./pages/WorkspaceSettings";
import Workload from "./pages/Workload";
import Risk from "./pages/Risk";
import MyWork from "./pages/MyWork";
import Projects from "./pages/Projects";
import Issues from "./pages/Issues";
import IssueDetail from "./pages/IssueDetail";
import Intake from "./pages/Intake";
import Notifications from "./pages/Notifications";
import Profile from "./pages/Profile";
import ApiTokens from "./pages/ApiTokens";
import Board from "./pages/Board";
import Calendar from "./pages/Calendar";
import Overview from "./pages/Overview";
import Gantt from "./pages/Gantt";
import Roadmap from "./pages/Roadmap";
import Analytics from "./pages/Analytics";
import Activity from "./pages/Activity";
import ProjectSettings from "./pages/ProjectSettings";
import Drafts from "./pages/Drafts";
import Archived from "./pages/Archived";
import Trash from "./pages/Trash";
import Cycles from "./pages/Cycles";
import Modules from "./pages/Modules";
import Epics from "./pages/Epics";
import EpicDetail from "./pages/EpicDetail";
import Initiatives from "./pages/Initiatives";
import InitiativeDetail from "./pages/InitiativeDetail";
import Goals from "./pages/Goals";
import GoalDetail from "./pages/GoalDetail";
import Dashboards from "./pages/Dashboards";
import DashboardDetail from "./pages/DashboardDetail";
import Releases from "./pages/Releases";
import Schedules from "./pages/Schedules";
import Pages from "./pages/Pages";
import Spreadsheet from "./pages/Spreadsheet";
import PublicShare from "./pages/PublicShare";
import PublicIntakeForm from "./pages/PublicIntakeForm";
import ShortcutsHost from "./components/ShortcutsHost";
import AppShell from "./components/shell/AppShell";
import { Toaster } from "./components/ui/toast";

export default function App() {
  return (
    <AuthProvider>
      <QueryProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/s/:token" element={<PublicShare />} />
            {/* C82T1: public intake-form submission page (no auth). */}
            <Route path="/f/:token" element={<PublicIntakeForm />} />
            <Route element={<ProtectedRoute />}>
              {/* C44T0: GlobalScope owns the user:{id} subscription +
                  the single ConnectionDot for every authenticated
                  route, including /onboarding and the slug-less
                  AppShell routes (/, /notifications, /profile, …). */}
              <Route element={<GlobalScope />}>
              <Route path="/onboarding" element={<Onboarding />} />
              <Route element={<AppShell />}>
                <Route path="/" element={<Home />} />
                <Route path="/notifications" element={<Notifications />} />
                <Route path="/profile" element={<Profile />} />
                <Route path="/settings/tokens" element={<ApiTokens />} />
                <Route
                  path="/admin"
                  element={
                    <AdminGuard>
                      <Admin />
                    </AdminGuard>
                  }
                />
                <Route path="/w" element={<Workspaces />} />
                {/* C43T0: every /w/:slug/* route lives under WorkspaceScope
                    (workspace realtime mount). The single ConnectionDot
                    lives in GlobalScope (C44T0) above. */}
                <Route element={<WorkspaceScope />}>
                  <Route path="/w/:slug" element={<Projects />} />
                  <Route
                    path="/w/:slug/settings"
                    element={<WorkspaceSettings />}
                  />
                  <Route path="/w/:slug/my-work" element={<MyWork />} />
                  <Route
                    path="/w/:slug/initiatives"
                    element={<Initiatives />}
                  />
                  {/* C27T2: workspace-scoped initiative detail. */}
                  <Route
                    path="/w/:slug/initiatives/:id"
                    element={<InitiativeDetail />}
                  />
                  {/* C35T1: workspace-scoped OKR goals. */}
                  <Route path="/w/:slug/goals" element={<Goals />} />
                  <Route path="/w/:slug/goals/:id" element={<GoalDetail />} />
                  {/* C81T1: workspace-scoped dashboards. */}
                  <Route
                    path="/w/:slug/dashboards"
                    element={<Dashboards />}
                  />
                  <Route
                    path="/w/:slug/dashboards/:id"
                    element={<DashboardDetail />}
                  />
                  {/* C29T1: workspace-level workload & capacity. */}
                  <Route path="/w/:slug/workload" element={<Workload />} />
                  {/* C30T3: workspace-level risk radar. */}
                  <Route path="/w/:slug/risk" element={<Risk />} />
                  <Route element={<ProjectScope />}>
                    <Route path="/w/:slug/p/:identifier" element={<Issues />} />
                    <Route
                      path="/w/:slug/p/:identifier/overview"
                      element={<Overview />}
                    />
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
                      path="/w/:slug/p/:identifier/roadmap"
                      element={<Roadmap />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/analytics"
                      element={<Analytics />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/cycles"
                      element={<Cycles />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/modules"
                      element={<Modules />}
                    />
                    {/* C71T1: project-scoped epics. */}
                    <Route
                      path="/w/:slug/p/:identifier/epics"
                      element={<Epics />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/epics/:epicID"
                      element={<EpicDetail />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/releases"
                      element={<Releases />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/schedules"
                      element={<Schedules />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/pages"
                      element={<Pages />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/activity"
                      element={<Activity />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/settings"
                      element={<ProjectSettings />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/drafts"
                      element={<Drafts />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/archived"
                      element={<Archived />}
                    />
                    <Route
                      path="/w/:slug/p/:identifier/trash"
                      element={<Trash />}
                    />
                  </Route>
                </Route>
              </Route>
              </Route>
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
          <ShortcutsHost />
          <Toaster />
        </BrowserRouter>
      </QueryProvider>
    </AuthProvider>
  );
}
