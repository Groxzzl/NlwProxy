package tuiapp

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"nlwproxy/internal/metrics"
)

// Store is a small state bridge that combines application-owned gateway/profile
// fields with the existing metrics EventBus change generation.
type Store struct {
	mu       sync.RWMutex
	events   *metrics.EventBus
	snapshot Snapshot
	changed  chan struct{}
}

func NewStore(events *metrics.EventBus, initial Snapshot) *Store {
	return &Store{events: events, snapshot: initial, changed: make(chan struct{})}
}

func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	result := s.snapshot
	s.mu.RUnlock()
	if s.events != nil {
		metricsSnapshot := s.events.Snapshot()
		result.Requests = metricsSnapshot.Total
		result.Errors = metricsSnapshot.Errors
		result.Active = metricsSnapshot.Active
	}
	return result
}

// Set updates shell state and closes the current change generation.
func (s *Store) Set(snapshot Snapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.snapshot = snapshot
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *Store) Changes() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	local := s.changed
	s.mu.RUnlock()
	if s.events == nil {
		return local
	}
	_, events := s.events.Changes()
	combined := make(chan struct{})
	go func() {
		select {
		case <-local:
		case <-events:
		}
		close(combined)
	}()
	return combined
}

// ChangedMsg is useful to integrations that need to inject an already-loaded
// snapshot directly into the program.
func ChangedMsg(snapshot Snapshot) tea.Msg { return stateChangedMsg{snapshot: snapshot} }

// ContextSource adapts a snapshot function and renewable change-channel
// function without coupling gateway/profile packages to Bubble Tea.
type ContextSource struct {
	SnapshotFunc func() Snapshot
	ChangesFunc  func() <-chan struct{}
}

func (s ContextSource) Snapshot() Snapshot {
	if s.SnapshotFunc == nil {
		return Snapshot{}
	}
	return s.SnapshotFunc()
}

func (s ContextSource) Changes() <-chan struct{} {
	if s.ChangesFunc == nil {
		return nil
	}
	return s.ChangesFunc()
}

var _ StateSource = (*Store)(nil)
var _ StateSource = ContextSource{}
