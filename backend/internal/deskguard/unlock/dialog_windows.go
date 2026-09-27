// Password dialog copied from Computer Security (DeskGuard), commit
// ee5aecdc91450dca44847ba05e72f4b8039ad8cd, internal/gui/gui_windows.go.
// The owner, credential provider and control-lifetime cancellation are adapted
// for an interactive agent without a Walk main window.
package unlock

import (
	"context"
	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"runtime"
	"sync"
)

type credentialVerifier func(string) bool

func (verify credentialVerifier) CheckPasswordOrRecovery(secret string) bool { return verify(secret) }

type App struct {
	mw   walk.Form
	icon walk.Image
	cfg  credentialVerifier
	ctx  context.Context
}

var dialogMu sync.Mutex

func Run(ctx context.Context, verify func(string) bool) bool {
	dialogMu.Lock()
	defer dialogMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := &App{ctx: ctx, cfg: credentialVerifier(verify)}
	return app.passwordDialog()
}

func (a *App) prepDialog(dlg *walk.Dialog, focusOn walk.Widget) {
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	dlg.Activating().Attach(func() {
		h := dlg.Handle()
		centerTopmost(h)
		win.SetForegroundWindow(h)
		win.SetActiveWindow(h)
		if focusOn != nil {
			focusOn.SetFocus()
		}
	})
}

// centerTopmost 把窗口移到屏幕正中并置顶（最顶层）。
func centerTopmost(h win.HWND) {
	var r win.RECT
	if !win.GetWindowRect(h, &r) {
		return
	}
	w := r.Right - r.Left
	ht := r.Bottom - r.Top
	sx := win.GetSystemMetrics(win.SM_CXSCREEN)
	sy := win.GetSystemMetrics(win.SM_CYSCREEN)
	x := (sx - w) / 2
	y := (sy - ht) / 2
	win.SetWindowPos(h, win.HWND_TOPMOST, x, y, 0, 0, win.SWP_NOSIZE)
}

func (a *App) passwordDialog() bool {
	var dlg *walk.Dialog
	var pwEdit *walk.LineEdit
	var okBtn, cancelBtn *walk.PushButton
	result := false

	err := dcl.Dialog{
		AssignTo:      &dlg,
		Title:         "解除保护",
		MinSize:       dcl.Size{Width: 320, Height: 150},
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		Layout:        dcl.VBox{},
		Children: []dcl.Widget{
			dcl.Label{Text: "请输入解锁密码（忘记可输恢复码）：", Font: dcl.Font{Family: "Microsoft YaHei", PointSize: 10}},
			dcl.LineEdit{
				AssignTo:     &pwEdit,
				PasswordMode: true,
			},
			dcl.Composite{
				Layout: dcl.HBox{},
				Children: []dcl.Widget{
					dcl.HSpacer{},
					dcl.PushButton{
						AssignTo: &okBtn,
						Text:     "解锁",
						OnClicked: func() {
							if a.cfg.CheckPasswordOrRecovery(pwEdit.Text()) {
								result = true
								dlg.Accept()
							} else {
								walk.MsgBox(dlg, "验证失败", "密码或恢复码不正确，请重试。", walk.MsgBoxIconError)
								pwEdit.SetText("")
							}
						},
					},
					dcl.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "打开失败", err.Error(), walk.MsgBoxIconError)
		return false
	}
	if a.ctx.Err() != nil {
		dlg.Dispose()
		return false
	}
	handle := dlg.Handle()
	stopCancellation := context.AfterFunc(a.ctx, func() { win.PostMessage(handle, win.WM_CLOSE, 0, 0) })
	defer stopCancellation()
	a.prepDialog(dlg, pwEdit)
	restore := a.hardenPasswordInput(dlg, pwEdit)
	defer restore()
	dlg.Run()

	return result
}
