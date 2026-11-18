import { describe, expect, it } from "vitest"
import { averageHistogramMs, parseMetrics, sumWhere } from "@/lib/metrics"

const SAMPLE = `# HELP activity_descriptor_http_requests_total HTTP requests processed.
# TYPE activity_descriptor_http_requests_total counter
activity_descriptor_http_requests_total{method="GET",route="/api/v1/runs",status="200"} 10
activity_descriptor_http_requests_total{method="POST",route="/api/v1/runs",status="500"} 2
activity_descriptor_http_request_duration_seconds_sum{route="/api/v1/runs"} 1.5
activity_descriptor_http_request_duration_seconds_count{route="/api/v1/runs"} 3
activity_descriptor_agent_calls_total{agent="interviewer"} 4
activity_descriptor_agent_errors_total{agent="interviewer"} 1
activity_descriptor_agent_latency_seconds_sum{agent="interviewer"} 8
activity_descriptor_agent_latency_seconds_count{agent="interviewer"} 4
`

describe("парсер метрик", () => {
  it("разбирает имена, метки и значения", () => {
    const samples = parseMetrics(SAMPLE)

    const total = samples.find((sample) => sample.name === "activity_descriptor_http_requests_total")
    expect(total).toBeDefined()
    expect(total?.labels).toEqual({ method: "GET", route: "/api/v1/runs", status: "200" })
    expect(total?.value).toBe(10)
  })

  it("складывает счётчики с фильтром", () => {
    const samples = parseMetrics(SAMPLE)

    expect(sumWhere(samples, "activity_descriptor_http_requests_total")).toBe(12)
    expect(
      sumWhere(
        samples,
        "activity_descriptor_http_requests_total",
        (sample) => sample.labels.status === "500",
      ),
    ).toBe(2)
  })

  it("считает среднюю латентность из гистограммы", () => {
    const samples = parseMetrics(SAMPLE)

    expect(averageHistogramMs(samples, "activity_descriptor_http_request_duration_seconds")).toBe(500)
    expect(
      averageHistogramMs(
        samples,
        "activity_descriptor_agent_latency_seconds",
        (sample) => sample.labels.agent === "interviewer",
      ),
    ).toBe(2000)
    expect(averageHistogramMs(samples, "activity_descriptor_missing_seconds")).toBeNull()
  })

  it("игнорирует комментарии и мусор", () => {
    expect(parseMetrics("# TYPE foo counter\nnot a metric line\nfoo 1")).toEqual([
      { name: "foo", labels: {}, value: 1 },
    ])
  })
})
