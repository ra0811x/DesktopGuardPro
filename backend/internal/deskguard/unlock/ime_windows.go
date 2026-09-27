// 本文件解决"保护期弹出的密码框在中文输入法下无法正确接收密码/恢复码"的问题。
//
// 触发解锁（组合键/连点）在钩子层用原始虚拟键码判断，不受输入法影响；
// 真正受影响的是解锁后往密码框里输密码/恢复码这一步：中文输入法会把
// 敲入的字母转成拼音候选或中文，导致校验失败，表现为"收不到恢复指令"。
//
// 这里用两道叠加的措施保证密码框只接收原始 ASCII：
//   - 方案A：ImmAssociateContext(hwnd, 0) 关掉输入框自身的 IME 关联。
//   - 方案B：密码框激活时把当前线程键盘布局强制为英语(美国)，关闭时还原。
package unlock

import (
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

var (
	imm32                   = syscall.NewLazyDLL("imm32.dll")
	procImmAssociateContext = imm32.NewProc("ImmAssociateContext")

	user32Ime                  = syscall.NewLazyDLL("user32.dll")
	procLoadKeyboardLayoutW    = user32Ime.NewProc("LoadKeyboardLayoutW")
	procActivateKeyboardLayout = user32Ime.NewProc("ActivateKeyboardLayout")
	procGetKeyboardLayout      = user32Ime.NewProc("GetKeyboardLayout")
)

// klfActivate 让加载的键盘布局立即成为当前线程的活动布局。
const klfActivate = 0x00000001

// englishLayoutID 是美国英语键盘布局标识("00000409")，纯 ASCII 输入，无 IME。
const englishLayoutID = "00000409"

// disableIMEForWindow 断开指定窗口与 IME 上下文的关联，使其只接收原始 ASCII。
// 窗口销毁后关联自然失效，通常无需恢复。句柄为 0 时安全返回。
func disableIMEForWindow(h win.HWND) {
	if h == 0 {
		return
	}
	procImmAssociateContext.Call(uintptr(h), 0)
}

// forceEnglishLayout 记住当前线程的键盘布局并切换为英语(美国)。
// 返回原布局句柄供 restoreLayout 还原；加载失败返回 0（不改动布局）。
func forceEnglishLayout() uintptr {
	prev, _, _ := procGetKeyboardLayout.Call(0) // 0 = 当前线程
	name, err := syscall.UTF16PtrFromString(englishLayoutID)
	if err != nil {
		return 0
	}
	hkl, _, _ := procLoadKeyboardLayoutW.Call(uintptr(unsafe.Pointer(name)), klfActivate)
	if hkl == 0 {
		return 0
	}
	procActivateKeyboardLayout.Call(hkl, 0)
	return prev
}

// restoreLayout 还原到之前记住的键盘布局；prev 为 0 时不处理。
func restoreLayout(prev uintptr) {
	if prev == 0 {
		return
	}
	procActivateKeyboardLayout.Call(prev, 0)
}

// hardenPasswordInput 给密码/恢复码输入框加固，抵御中文输入法干扰：
//   - 立即关闭各输入框的 IME 关联（方案A）。
//   - 对话框首次激活时强制英语键盘布局（方案B）。
//
// 返回一个还原闭包，调用方应在 dlg.Run() 返回后调用它还原键盘布局。
func (a *App) hardenPasswordInput(dlg *walk.Dialog, edits ...*walk.LineEdit) func() {
	for _, e := range edits {
		if e != nil {
			disableIMEForWindow(e.Handle())
		}
	}
	var prevLayout uintptr
	var switched bool
	dlg.Activating().Attach(func() {
		if switched {
			return
		}
		prevLayout = forceEnglishLayout()
		switched = true
	})
	return func() { restoreLayout(prevLayout) }
}
