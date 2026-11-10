import { BrowserRouter, Routes, Route } from "react-router-dom"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { NewResearch } from "@/pages/NewResearch"
import { Research } from "@/pages/Research"

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="research/new" element={<NewResearch />} />
          <Route path="research/:id" element={<Research />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App