package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/domain"
)

func main() {
	_ = os.Setenv("SSE_HEARTBEAT_S", "2")

	srv := api.NewServerForDemo()
	id := srv.DemoRunID()

	go func() {
		seq := []struct {
			stage  domain.Stage
			status domain.RunStatus
		}{
			{domain.StageInterview, domain.RunRunning},
			{domain.StageAnalysis, domain.RunRunning},
			{domain.StageSynthesis, domain.RunRunning},
			{domain.StageReview, domain.RunRunning},
			{domain.StageDone, domain.RunDone},
		}
		for _, step := range seq {
			time.Sleep(3 * time.Second)
			_ = srv.DemoSetStage(id, step.stage)
			_ = srv.DemoSetStatus(id, step.status)
			fmt.Println("stage ->", step.stage, "status ->", step.status)
		}
	}()

	fmt.Printf("curl -N -H \"Authorization: Bearer t\" http://localhost:8080/api/v1/runs/%s/events\n", id)
	fmt.Println("(heartbeat ~2s; stream closes on done)")
	log.Fatal(http.ListenAndServe(":8080", srv.Routes()))
}
