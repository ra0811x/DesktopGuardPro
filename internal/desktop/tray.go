package desktop

import (
	"sync"

	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

type Lifecycle struct {
	showWindow func()
	hideWindow func()
	quitApp    func()

	mutex    sync.RWMutex
	quitting bool
}

func NewLifecycle(showWindow, hideWindow, quitApp func()) *Lifecycle {
	return &Lifecycle{
		showWindow: showWindow,
		hideWindow: hideWindow,
		quitApp:    quitApp,
	}
}

func (lifecycle *Lifecycle) ShowWindow() {
	lifecycle.showWindow()
}

func (lifecycle *Lifecycle) HandleWindowClose(cancel func()) {
	lifecycle.mutex.RLock()
	quitting := lifecycle.quitting
	lifecycle.mutex.RUnlock()
	if quitting {
		return
	}

	lifecycle.hideWindow()
	cancel()
}

func (lifecycle *Lifecycle) Quit() {
	lifecycle.mutex.Lock()
	if lifecycle.quitting {
		lifecycle.mutex.Unlock()
		return
	}
	lifecycle.quitting = true
	lifecycle.mutex.Unlock()
	lifecycle.quitApp()
}

func TrayStatusLabel(health coreservice.HealthResult, err error) string {
	if err != nil {
		return "状态：后台服务离线"
	}
	if health.Status != coreservice.HealthStatusRunning {
		return "状态：后台服务异常"
	}
	if health.Session == nil {
		return "状态：等待开启保护"
	}

	var state string
	switch health.Session.State {
	case domain.SessionStateDraft:
		state = "会话草稿"
	case domain.SessionStatePreparing:
		state = "正在准备"
	case domain.SessionStateActive, domain.SessionStateDegraded:
		state = "保护中"
	case domain.SessionStateFinalizing:
		state = "正在结束"
	case domain.SessionStateCompleted:
		state = "最近会话已完成"
	case domain.SessionStateFailed:
		state = "最近会话失败"
	default:
		state = "会话状态未知"
	}
	if health.Session.Name == "" {
		return "状态：" + state
	}
	return "状态：" + state + " · " + health.Session.Name
}
