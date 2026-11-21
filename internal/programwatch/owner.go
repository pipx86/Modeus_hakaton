package programwatch

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultLease — срок lease-блокировки по умолчанию. Он заметно больше
// таймаута HTTP-запроса и разбора документа, но достаточно мал, чтобы после
// падения воркера источник снова стал доступен без ручного вмешательства.
const DefaultLease = 20 * time.Minute

var ownerSequence atomic.Uint64

// DefaultOwner возвращает идентификатор владельца блокировки по умолчанию:
// hostname-pid. Этого достаточно, чтобы различать воркеры на разных хостах и
// процессы на одном хосте; уникальность внутри процесса добавляет NewOwner.
func DefaultOwner() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// NewOwner возвращает уникальный идентификатор владельца с префиксом. API
// использует его на каждый запрос, чтобы Release одной проверки не мог снять
// блокировку, перехваченную другой проверкой того же процесса.
func NewOwner(prefix string) string {
	owner := DefaultOwner()
	if trimmed := strings.TrimSpace(prefix); trimmed != "" {
		owner = trimmed + "-" + owner
	}
	return fmt.Sprintf("%s-%d", owner, ownerSequence.Add(1))
}
