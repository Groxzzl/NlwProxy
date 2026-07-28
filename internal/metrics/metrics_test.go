package metrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventBusIsBoundedAndSnapshotsAreImmutable(t *testing.T) {
	bus := NewEventBus(2)
	bus.Publish(Request{RequestID: "1", Endpoint: "/v1/responses", RequestedModel: "model-a", RouteID: "a", Status: 200})
	bus.Publish(Request{RequestID: "2", Endpoint: "/v1/responses", RequestedModel: "model-b", RouteID: "b", Status: 503})
	bus.Publish(Request{RequestID: "3", Endpoint: "/v1/chat/completions", RequestedModel: "model-c", RouteID: "c", Status: 200})

	snapshot := bus.Snapshot()
	if snapshot.Total != 3 || snapshot.Errors != 1 || snapshot.Active != 0 || len(snapshot.Events) != 2 || snapshot.Events[0].RequestID != "2" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	snapshot.Events[0].RouteID = "mutated"
	if got := bus.Snapshot().Events[0].RouteID; got != "b" {
		t.Fatalf("snapshot leaked mutable storage: %q", got)
	}
}

func TestEventBusTracksActiveRequestsAndNeverSerializesContent(t *testing.T) {
	bus := NewEventBus(4)
	bus.Start()
	if got := bus.Snapshot().Active; got != 1 {
		t.Fatalf("active=%d", got)
	}
	bus.Publish(Request{RequestID: "1", Endpoint: "/v1/responses", Prompt: "TOP SECRET", Response: "PRIVATE", RequestedModel: "gpt-test"})
	data, err := json.Marshal(bus.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TOP SECRET") || strings.Contains(string(data), "PRIVATE") {
		t.Fatalf("content leaked: %s", data)
	}
}

func TestEventBusRevisionAndChangesUnderConcurrency(t *testing.T) {
	bus := NewEventBus(32)
	initial, changed := bus.Changes()
	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Start()
			bus.Publish(Request{Endpoint: "/v1/responses", RequestedModel: "model", Prompt: "private", Response: "private"})
		}()
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("metadata change was not signaled")
	}
	wg.Wait()
	snapshot := bus.Snapshot()
	if snapshot.Revision != initial+workers*2 || snapshot.Total != workers || snapshot.Active != 0 {
		t.Fatalf("snapshot=%+v initial revision=%d", snapshot, initial)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") {
		t.Fatalf("content leaked: %s", data)
	}
	current, next := bus.Changes()
	if current != snapshot.Revision {
		t.Fatalf("changes revision=%d snapshot revision=%d", current, snapshot.Revision)
	}
	select {
	case <-next:
		t.Fatal("new change channel was already closed")
	default:
	}
}

func TestJSONLStorePersistsMetadataOnlyAndBoundsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.jsonl")
	store, err := NewJSONLStore(path, 700)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		err := store.Append(context.Background(), Request{RequestID: strings.Repeat("r", 20), SessionHash: "saltedhash", RouteID: "direct", Endpoint: "/v1/responses", Status: 200, StartedAt: time.Unix(int64(i), 0).UTC(), TTFT: time.Millisecond, Duration: time.Second, RequestBytes: 10, ResponseBytes: 20, RetryCount: 0, ErrorCode: ""})
		if err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 700 {
		t.Fatalf("file is unbounded: %d", info.Size())
	}
	data, _ := os.ReadFile(path)
	for _, forbidden := range []string{"prompt", "response\"", "authorization", "api_key"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("content leaked: %s", data)
		}
	}
	items, err := store.Recent(context.Background(), 3)
	if err != nil || len(items) == 0 || len(items) > 3 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
}

func TestStoreRejectsUnsafeMetadata(t *testing.T) {
	store, err := NewJSONLStore(filepath.Join(t.TempDir(), "m.jsonl"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), Request{RequestID: "1", Endpoint: "private prompt text"}); err == nil {
		t.Fatal("expected endpoint validation")
	}
}
