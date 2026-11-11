package sse

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

type EventType string

const (
	EventStageChange EventType = "stage_change"
	EventProgress    EventType = "progress"
	EventError       EventType = "error"
	EventDone        EventType = "done"
	EventHeartbeat   EventType = "heartbeat"
)

type SSEEvent struct {
	ID        string    `json:"-"`
	Type      EventType `json:"event,omitempty"`
	RunID     string    `json:"run_id"`
	Payload   any       `json:"data"`
	Timestamp time.Time `json:"ts"`
}

func (e *SSEEvent) Encode() ([]byte, error) {
	dataBytes, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	var buf []byte
	buf = append(buf, []byte("id: "+e.ID+"\n")...)
	if e.Type != "" {
		buf = append(buf, []byte("event: "+string(e.Type)+"\n")...)
	}
	buf = append(buf, []byte("data: "+string(dataBytes)+"\n\n")...)
	return buf, nil
}

var seqCounter uint64

func generateID() string {
	n := atomic.AddUint64(&seqCounter, 1)
	return fmt.Sprintf("evt-%d-%d", time.Now().UnixMilli(), n)
}

func NewEvent(runID string, typ EventType, payload any) *SSEEvent {
	return &SSEEvent{
		ID:        generateID(),
		Type:      typ,
		RunID:     runID,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}
}
