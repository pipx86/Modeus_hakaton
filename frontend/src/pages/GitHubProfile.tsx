/**
 * @file GitHubProfile.tsx
 * @description Оценка специалиста по публичным репозиториям GitHub.
 */

import { useState, type FormEvent } from "react"
import {
  ExternalLinkIcon,
  GitBranchIcon,
  GitForkIcon,
  SearchIcon,
  StarIcon,
} from "lucide-react"
import { ApiRequestError, evaluateGitHub, type GitHubAssessment } from "@/lib/api/client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"

function scoreTone(score: number): string {
  if (score >= 70) return "text-success"
  if (score >= 40) return "text-warning"
  return "text-destructive"
}

function formatDate(value?: string): string {
  if (!value) return "дата не указана"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? "дата не указана" : date.toLocaleDateString("ru-RU")
}

export function GitHubProfile() {
  const [username, setUsername] = useState("")
  const [profession, setProfession] = useState("")
  const [assessment, setAssessment] = useState<GitHubAssessment | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const login = username.trim().replace(/^@/, "")
    if (!login) {
      setError("Укажите имя пользователя GitHub.")
      return
    }
    setLoading(true)
    setError(null)
    try {
      setAssessment(await evaluateGitHub(login, profession.trim()))
    } catch (cause) {
      const message =
        cause instanceof ApiRequestError
          ? cause.message
          : "Не удалось получить профиль GitHub. Проверьте имя пользователя и повторите."
      setError(message)
      setAssessment(null)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">GitHub-профиль</h1>
        <p className="text-muted-foreground">
          Оценка специалиста по публичным репозиториям: языки, темы, активность и совпадение с профессией.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Проверить профиль</CardTitle>
          <CardDescription>
            Укажите GitHub username и, при необходимости, профессию для сравнения. Используются только публичные данные.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4 md:grid-cols-[1fr_1fr_auto] md:items-end" onSubmit={submit}>
            <div className="space-y-2">
              <Label htmlFor="github-username">GitHub username</Label>
              <Input
                id="github-username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="например, octocat"
                autoComplete="off"
                disabled={loading}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="github-profession">Профессия</Label>
              <Input
                id="github-profession"
                value={profession}
                onChange={(event) => setProfession(event.target.value)}
                placeholder="например, Backend-разработчик"
                disabled={loading}
              />
            </div>
            <Button type="submit" disabled={loading}>
              <SearchIcon className="size-4" />
              {loading ? "Проверяем..." : "Оценить"}
            </Button>
          </form>
          {error && (
            <p className="mt-4 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </p>
          )}
        </CardContent>
      </Card>

      {loading && (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {Array.from({ length: 4 }).map((_, index) => (
            <Skeleton key={index} className="h-28 w-full" />
          ))}
        </div>
      )}

      {assessment && (
        <div className="space-y-4">
          <Card>
            <CardContent className="flex flex-wrap items-start justify-between gap-4 pt-6">
              <div className="flex min-w-0 items-center gap-4">
                {assessment.avatar_url ? (
                  <img
                    src={assessment.avatar_url}
                    alt=""
                    className="size-14 rounded-full border"
                    referrerPolicy="no-referrer"
                  />
                ) : (
                  <div className="flex size-14 items-center justify-center rounded-full border bg-muted">
                    <GitBranchIcon className="size-7" />
                  </div>
                )}
                <div className="min-w-0 space-y-1">
                  <h2 className="truncate text-xl font-semibold">
                    {assessment.name || assessment.username}
                  </h2>
                  <a
                    className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
                    href={assessment.profile_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    @{assessment.username}
                    <ExternalLinkIcon className="size-3" />
                  </a>
                </div>
              </div>
              <div className="text-right">
                <p className={`text-4xl font-semibold ${scoreTone(assessment.score)}`}>{assessment.score}</p>
                <p className="text-xs text-muted-foreground">из 100 за профиль</p>
                {assessment.profession && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    Совпадение с профессией: {assessment.profession_matched ? `${assessment.profession_match}%` : "не найдено"}
                  </p>
                )}
              </div>
            </CardContent>
            <CardContent className="space-y-3 pt-0">
              <p className="text-sm">{assessment.summary}</p>
              {assessment.warnings?.map((warning) => (
                <p key={warning} className="text-xs text-warning">
                  {warning}
                </p>
              ))}
            </CardContent>
          </Card>

          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {[
              ["Репозитории", assessment.original_repositories],
              ["Звёзды", assessment.total_stars],
              ["Форки", assessment.total_forks],
              ["Языки", assessment.languages.length],
            ].map(([label, value]) => (
              <Card key={String(label)}>
                <CardContent className="pt-5">
                  <p className="text-xs uppercase tracking-wide text-muted-foreground">{label}</p>
                  <p className="mt-1 text-2xl font-semibold">{value}</p>
                </CardContent>
              </Card>
            ))}
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Языки и навыки</CardTitle>
                <CardDescription>По языкам репозиториев, темам и описаниям проектов.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex flex-wrap gap-2">
                  {assessment.languages.length === 0 ? (
                    <p className="text-sm text-muted-foreground">Языки не указаны.</p>
                  ) : (
                    assessment.languages.map((language) => (
                      <Badge key={language.name} variant="secondary">
                        {language.name} · {language.repositories}
                      </Badge>
                    ))
                  )}
                </div>
                <div className="flex flex-wrap gap-2">
                  {assessment.skills.map((skill) => (
                    <Badge key={skill} variant="outline">{skill}</Badge>
                  ))}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-base">Лучшие проекты</CardTitle>
                <CardDescription>Отсортированы по звёздам и дате обновления.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                {assessment.projects.length === 0 ? (
                  <p className="text-sm text-muted-foreground">Публичных проектов не найдено.</p>
                ) : (
                  assessment.projects.map((project) => (
                    <div key={project.url} className="rounded-lg border p-3">
                      <div className="flex flex-wrap items-start justify-between gap-2">
                        <a
                          className="inline-flex items-center gap-1 font-medium hover:underline"
                          href={project.url}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {project.name}
                          <ExternalLinkIcon className="size-3" />
                        </a>
                        <div className="flex items-center gap-3 text-xs text-muted-foreground">
                          <span className="inline-flex items-center gap-1"><StarIcon className="size-3" />{project.stars}</span>
                          <span className="inline-flex items-center gap-1"><GitForkIcon className="size-3" />{project.forks}</span>
                        </div>
                      </div>
                      {project.description && <p className="mt-1 text-sm text-muted-foreground">{project.description}</p>}
                      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        {project.language && <Badge variant="secondary">{project.language}</Badge>}
                        {project.topics?.slice(0, 4).map((topic) => <Badge key={topic} variant="outline">{topic}</Badge>)}
                        <span>обновлён {formatDate(project.updated_at)}</span>
                      </div>
                    </div>
                  ))
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </div>
  )
}
