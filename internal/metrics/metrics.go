// Package metrics persists bounded request metadata without request or response content.
package metrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RequestState string

const (
	RequestActive    RequestState = "active"
	RequestStreaming RequestState = "streaming"
	RequestCompleted RequestState = "completed"
)

type Request struct {
	RequestID      string        `json:"request_id"`
	SessionHash    string        `json:"session_hash,omitempty"`
	RouteID        string        `json:"route_id,omitempty"`
	Endpoint       string        `json:"endpoint"`
	RequestedModel string        `json:"requested_model,omitempty"`
	InputTokens    int64         `json:"input_tokens,omitempty"`
	OutputTokens   int64         `json:"output_tokens,omitempty"`
	TotalTokens    int64         `json:"total_tokens,omitempty"`
	Status         int           `json:"status"`
	StartedAt      time.Time     `json:"started_at,omitempty"`
	TTFT           time.Duration `json:"ttft_ns,omitempty"`
	Duration       time.Duration `json:"duration_ns,omitempty"`
	RequestBytes   int64         `json:"request_bytes,omitempty"`
	ResponseBytes  int64         `json:"response_bytes,omitempty"`
	RetryCount     int           `json:"retry_count,omitempty"`
	ErrorCode      string        `json:"error_code,omitempty"`
	State          RequestState  `json:"state"`
	Prompt         string        `json:"-"`
	Response       string        `json:"-"`
}

type Snapshot struct {
	Events   []Request `json:"events"`
	Total    int64     `json:"total"`
	Errors   int64     `json:"errors"`
	Active   int64     `json:"active"`
	Revision uint64    `json:"revision"`
}

// EventBus is a bounded, concurrency-safe live metadata feed.
type EventBus struct {
	mu                    sync.RWMutex
	capacity              int
	events                []Request
	total, errors, active int64
	revision              uint64
	changed               chan struct{}
}

func NewEventBus(capacity int) *EventBus {
	if capacity <= 0 {
		capacity = 256
	}
	return &EventBus{capacity: capacity, events: make([]Request, 0, capacity), changed: make(chan struct{})}
}

func (b *EventBus) signalLocked() {
	b.revision++
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *EventBus) Start(events ...Request) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.active++
	if len(events) > 0 {
		b.upsertLocked(sanitize(events[0], RequestActive))
	}
	b.signalLocked()
	b.mu.Unlock()
}

func sanitize(event Request, state RequestState) Request {
	event.Prompt, event.Response, event.State = "", "", state
	return event
}

func (b *EventBus) upsertLocked(event Request) {
	for i := range b.events {
		if event.RequestID != "" && b.events[i].RequestID == event.RequestID {
			b.events[i] = event
			return
		}
	}
	if len(b.events) == b.capacity {
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = event
	} else {
		b.events = append(b.events, event)
	}
}

func (b *EventBus) Update(event Request) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.upsertLocked(sanitize(event, event.State))
	b.signalLocked()
}

func (b *EventBus) Publish(event Request) {
	if b == nil {
		return
	}
	event = sanitize(event, RequestCompleted)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total++
	if event.Status >= 400 || event.ErrorCode != "" {
		b.errors++
	}
	if b.active > 0 {
		b.active--
	}
	b.upsertLocked(event)
	b.signalLocked()
}

func (b *EventBus) Snapshot() Snapshot {
	if b == nil {
		return Snapshot{Events: []Request{}}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Snapshot{Events: append([]Request(nil), b.events...), Total: b.total, Errors: b.errors, Active: b.active, Revision: b.revision}
}

// Changes returns the current metadata revision and a channel closed by the
// next change. Callers re-read Changes after waking to coalesce any burst of
// request metadata updates into a single redraw.
func (b *EventBus) Changes() (uint64, <-chan struct{}) {
	if b == nil {
		return 0, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.revision, b.changed
}

type JSONLStore struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
}

func NewJSONLStore(path string, maxBytes int64) (*JSONLStore, error) {
	if maxBytes <= 0 {
		return nil, errors.New("max bytes must be positive")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return &JSONLStore{path: path, maxBytes: maxBytes}, nil
}

func (s *JSONLStore) Append(ctx context.Context, item Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !strings.HasPrefix(item.Endpoint, "/") || strings.ContainsAny(item.Endpoint, "\r\n \t") {
		return errors.New("endpoint must be a metadata-only path")
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if int64(len(data)) > s.maxBytes {
		return errors.New("metadata record exceeds store limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	combined := append(existing, data...)
	if int64(len(combined)) > s.maxBytes {
		// Preserve complete JSONL records and keep as many newest records as fit.
		// Searching from the raw byte cutoff may drop an extra complete record.
		lines := bytes.Split(combined, []byte{'\n'})
		if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}
		kept := make([][]byte, 0, len(lines))
		used := int64(0)
		for i := len(lines) - 1; i >= 0; i-- {
			lineSize := int64(len(lines[i]) + 1)
			if used+lineSize > s.maxBytes {
				break
			}
			kept = append(kept, lines[i])
			used += lineSize
		}
		for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
			kept[left], kept[right] = kept[right], kept[left]
		}
		combined = bytes.Join(kept, []byte{'\n'})
		if len(combined) > 0 {
			combined = append(combined, '\n')
		}
	}
	return os.WriteFile(s.path, combined, 0600)
}

func (s *JSONLStore) Recent(ctx context.Context, limit int) ([]Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []Request{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	items := make([]Request, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var item Request
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}
