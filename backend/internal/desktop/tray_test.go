package desktop

import (
	"errors"
	"testing"

	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

func TestLifecycleHidesWindowOnClose(t *testing.T) {
	t.Parallel()

	hidden := 0
	cancelled := 0
	controller := NewLifecycle(func() {}, func() { hidden++ }, func() {})
	controller.HandleWindowClose(func() { cancelled++ })

	if hidden != 1 || cancelled != 1 {
		t.Fatalf("close actions = hidden:%d cancelled:%d, want 1 each", hidden, cancelled)
	}
}

func TestLifecycleShowsAndExplicitlyQuits(t *testing.T) {
	t.Parallel()

	shown := 0
	quit := 0
	cancelled := 0
	controller := NewLifecycle(func() { shown++ }, func() {}, func() { quit++ })
	controller.ShowWindow()
	controller.Quit()
	controller.HandleWindowClose(func() { cancelled++ })

	if shown != 1 || quit != 1 {
		t.Fatalf("actions = shown:%d quit:%d, want 1 each", shown, quit)
	}
	if cancelled != 0 {
		t.Fatalf("explicit quit close was cancelled %d times", cancelled)
	}
}

func TestTrayStatusLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		health coreservice.HealthResult
		err    error
		want   string
	}{
		{name: "offline", err: errors.New("pipe unavailable"), want: "状态：后台服务离线"},
		{name: "idle", health: coreservice.HealthResult{Status: coreservice.HealthStatusRunning}, want: "状态：等待开启保护"},
		{
			name: "active",
			health: coreservice.HealthResult{
				Status:  coreservice.HealthStatusRunning,
				Session: &domain.Session{Name: "离席保护", State: domain.SessionStateActive},
			},
			want: "状态：保护中 · 离席保护",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := TrayStatusLabel(test.health, test.err); got != test.want {
				t.Fatalf("TrayStatusLabel() = %q, want %q", got, test.want)
			}
		})
	}
}
