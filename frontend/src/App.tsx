/**
 * @file App.tsx
 * @description Маршруты приложения, провайдеры темы и тостов, границы ошибок.
 */

import { BrowserRouter, Routes, Route } from "react-router-dom"
import { ErrorBoundary } from "@/components/error-boundary"
import { RequireAuth } from "@/components/require-auth"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { Admin } from "@/pages/admin"
import { Login } from "@/pages/login"
import { NewResearch } from "@/pages/NewResearch"
import { NotFound } from "@/pages/not-found"
import { Research } from "@/pages/Research"
import { CompareRuns } from "@/pages/CompareRuns"
import { GitHubProfile } from "@/pages/GitHubProfile"

function App() {
  return (
    <BrowserRouter>
      <ErrorBoundary>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route
              index
              element={
                <RequireAuth>
                  <ErrorBoundary key="dashboard" variant="inline">
                    <Dashboard />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route
              path="research/new"
              element={
                <RequireAuth>
                  <ErrorBoundary key="new-research" variant="inline">
                    <NewResearch />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route
              path="research/:id"
              element={
                <RequireAuth>
                  <ErrorBoundary key="research" variant="inline">
                    <Research />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route
              path="compare"
              element={
                <RequireAuth>
                  <ErrorBoundary key="compare" variant="inline">
                    <CompareRuns />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route
              path="github"
              element={
                <RequireAuth>
                  <ErrorBoundary key="github" variant="inline">
                    <GitHubProfile />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route
              path="login"
              element={
                <ErrorBoundary key="login" variant="inline">
                  <Login />
                </ErrorBoundary>
              }
            />
            <Route
              path="admin"
              element={
                <RequireAuth>
                  <ErrorBoundary key="admin" variant="inline">
                    <Admin />
                  </ErrorBoundary>
                </RequireAuth>
              }
            />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </ErrorBoundary>
    </BrowserRouter>
  )
}

export default App