// Команда local-token выпускает JWT для локальной проверки API; это не токен hh.ru.
package main

import (
	"activity-descriptor/internal/auth"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	role := flag.String("role", "teacher", "роль: admin, teacher, rop, student")
	subject := flag.String("subject", "local-demo", "идентификатор локального пользователя")
	ttl := flag.Duration("ttl", 15*time.Minute, "срок действия, не более суток")
	flag.Parse()
	if strings.TrimSpace(*subject) == "" || *ttl <= 0 || *ttl > 24*time.Hour {
		fmt.Fprintln(os.Stderr, "нужны пользователь и срок от нуля до суток")
		os.Exit(1)
	}
	token, err := auth.Issue(*subject, auth.Role(*role), *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(token)
}
