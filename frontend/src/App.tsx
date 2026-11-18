import { BrowserRouter, Routes, Route } from "react-router-dom"
import { ErrorBoundary } from "@/components/error-boundary"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { Login } from "@/pages/login"
import { NewResearch } from "@/pages/NewResearch"
import { NotFound } from "@/pages/not-found"
import { Research } from "@/pages/Research"

function App() {
  return (
    <BrowserRouter>
      <ErrorBoundary>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route
              index
              element={
                <ErrorBoundary key="dashboard" variant="inline">
                  <Dashboard />
                </ErrorBoundary>
              }
            />
            <Route
              path="research/new"
              element={
                <ErrorBoundary key="new-research" variant="inline">
                  <NewResearch />
                </ErrorBoundary>
              }
            />
            <Route
              path="research/:id"
              element={
                <ErrorBoundary key="research" variant="inline">
                  <Research />
                </ErrorBoundary>
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
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </ErrorBoundary>
    </BrowserRouter>
  )
}

export default App