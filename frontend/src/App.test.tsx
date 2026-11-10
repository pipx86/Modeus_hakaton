import "@/test/fetch-stub"
/// <reference types="@testing-library/jest-dom" />

import { render, screen } from "@testing-library/react"
import App from "./App"

test("renders the app shell with the product subtitle", () => {
  render(<App />)
  expect(screen.getByText("Анализ деятельности")).toBeInTheDocument()
})