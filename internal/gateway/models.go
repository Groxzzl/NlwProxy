package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// CachedModelService discovers the real upstream catalog through the same
// authorized route transport used by gateway requests. It never stores keys.
type CachedModelService struct {
	Transport http.RoundTripper
	TTL       time.Duration
	Now       func() time.Time
	mu        sync.Mutex
	models    []Model
	expires   time.Time
}

func (s *CachedModelService) Discover(ctx context.Context) ([]Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if len(s.models) > 0 && now.Before(s.expires) {
		return append([]Model(nil), s.models...), nil
	}
	if s.Transport == nil {
		return nil, errors.New("model transport unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://nlw.local/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, errors.New("upstream model catalog failed")
	}
	var payload struct {
		Data []struct{ ID, Object, OwnedBy, Name string } `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || id == "opencode-route" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, Model{ID: id, Name: item.Name, Object: item.Object, OwnedBy: item.OwnedBy})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	s.models, s.expires = append([]Model(nil), models...), now.Add(ttl)
	return append([]Model(nil), models...), nil
}

func (s *CachedModelService) Test(ctx context.Context, id string) ModelTest {
	models, err := s.Discover(ctx)
	if err != nil {
		return ModelTest{Model: id, ErrorCode: "model_discovery_failed"}
	}
	for _, model := range models {
		if model.ID == id {
			return ModelTest{Model: id, Available: true}
		}
	}
	return ModelTest{Model: id, Available: false, Status: http.StatusNotFound}
}
