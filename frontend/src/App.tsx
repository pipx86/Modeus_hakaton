import { BrowserRouter, Routes, Route } from "react-router-dom"
import { ErrorBoundary } from "@/components/error-boundary"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { NewResearch } from "@/pages/NewResearch"
import { NotFound } from "@/pages/not-found"
import { Research } from "@/pages/Research"

function App() {
  return (
    <BrowserRouter>
      <ErrorBoundary>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route index element={<Dashboard />} />
            <Route path="research/new" element={<NewResearch />} />
            <Route path="research/:id" element={<Research />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </ErrorBoundary>
    </BrowserRouter>
  )
}

export default App