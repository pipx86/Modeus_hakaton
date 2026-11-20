import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { setFetchHandler } from "@/test/fetch-stub"
import { GitHubProfile } from "@/pages/GitHubProfile"

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/github"]}>
      <GitHubProfile />
    </MemoryRouter>,
  )
}

describe("GitHubProfile", () => {
  it("показывает оценку публичного профиля", async () => {
    setFetchHandler(async (url) => {
      if (url.endsWith("/api/v1/github/evaluate")) {
        return {
          status: 200,
          body: {
            username: "octocat",
            name: "Octo Cat",
            profile_url: "https://github.com/octocat",
            score: 72,
            profession_match: 80,
            profession_matched: true,
            profession: "Backend-разработчик",
            repositories: 2,
            original_repositories: 2,
            total_stars: 50,
            total_forks: 4,
            languages: [{ name: "Go", repositories: 1 }, { name: "TypeScript", repositories: 1 }],
            skills: ["Go", "PostgreSQL", "React"],
            projects: [{
              name: "backend-service",
              url: "https://github.com/octocat/backend-service",
              description: "Go and PostgreSQL service",
              language: "Go",
              topics: ["docker"],
              stars: 42,
              forks: 3,
              updated_at: "2026-11-01T00:00:00Z",
            }],
            summary: "Публичных репозиториев: 2, звёзд: 50.",
            fetched_at: "2026-11-20T00:00:00Z",
          },
        }
      }
      return { status: 404, body: { code: "NOT_FOUND", message: "нет маршрута" } }
    })
    const user = userEvent.setup()
    renderPage()

    await user.type(screen.getByLabelText("GitHub username"), "octocat")
    await user.type(screen.getByLabelText("Профессия"), "Backend-разработчик")
    await user.click(screen.getByRole("button", { name: "Оценить" }))

    expect(await screen.findByText("Octo Cat")).toBeInTheDocument()
    expect(screen.getByText("72")).toBeInTheDocument()
    expect(screen.getByText("backend-service")).toBeInTheDocument()
    expect(screen.getByText(/Совпадение с профессией/)).toBeInTheDocument()
  })

  it("показывает ошибку для пустого имени", async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole("button", { name: "Оценить" }))
    expect(await screen.findByText("Укажите имя пользователя GitHub.")).toBeInTheDocument()
  })
})
