package main

import (
	"fmt"
	"net/http"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/logging"
)

func main() {
	logging.Init()
	srv := api.NewServerForDemo()
	handler := srv.Routes()

	fmt.Println("Starting demo server on :8080...")
	fmt.Println("Swagger UI: http://localhost:8080/swagger/")

	if err := http.ListenAndServe(":8080", handler); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}
