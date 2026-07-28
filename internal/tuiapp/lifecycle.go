package tuiapp

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/tuiapp/pages"
)

// Lifecycle owns the gateway started and stopped with the Bubble Tea program.
type Lifecycle interface {
	Source() StateSource
	Start(context.Context) error
	Stop(context.Context) error
}

type runtimeStartedMsg struct{ err error }
type runtimeStoppedMsg struct{ err error }

// NewRuntime creates a model whose Init starts the gateway before publishing
// ONLINE. Quit first drains the gateway, then exits Bubble Tea.
func NewRuntime(ctx context.Context, lifecycle Lifecycle) Model {
	var source StateSource
	if lifecycle != nil {
		source = lifecycle.Source()
	}
	m := New(ctx, source)
	m.lifecycle = lifecycle
	if provider, ok := lifecycle.(interface{ Operations() pages.OperationsSource }); ok {
		m.operations = provider.Operations()
		m.overview = pages.NewOperationsOverview(m.operations)
		m.modelsPage = pages.NewModelsWithOperations(nil, "", m.operations)
		m.requestsPage = pages.NewOperationsRequests(m.operations)
		m.logsPage = pages.NewLogs(m.operations)
	}
	if provider, ok := lifecycle.(interface {
		Proxies() pages.ProxyManagerSource
	}); ok {
		m.proxiesPage = pages.NewProxiesPage(provider.Proxies())
	} else {
		m.proxiesPage = pages.NewProxiesPage(nil)
	}
	if lifecycle != nil {
		m.snapshot.Status = "STARTING"
	}
	return m
}

func startRuntime(ctx context.Context, lifecycle Lifecycle) tea.Cmd {
	if lifecycle == nil {
		return nil
	}
	return func() tea.Msg { return runtimeStartedMsg{err: lifecycle.Start(ctx)} }
}

func stopRuntime(lifecycle Lifecycle) tea.Cmd {
	if lifecycle == nil {
		return tea.Quit
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return runtimeStoppedMsg{err: lifecycle.Stop(ctx)}
	}
}

func lifecycleSnapshot(current Snapshot, status string, err error) Snapshot {
	current.Status = status
	if err != nil {
		current.Notice = fmt.Sprintf("Gateway %s: %v", status, err)
	} else if status == "ONLINE" {
		current.Notice = "Gateway online."
	}
	return current
}
