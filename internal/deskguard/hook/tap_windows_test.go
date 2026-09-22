package hook

import (
	"testing"
	"time"
)

// TestRegisterTapKey 验证连点计数：达标返回 reached，非连点键不受影响。
func TestRegisterTapKey(t *testing.T) {
	e := New(Hotkey{})
	e.SetTap(0x1B, 3, time.Second) // ESC，连点 3 次，1 秒窗口

	// 非连点键：isTap=false。
	if isTap, _ := e.registerTapKey('A', true); isTap {
		t.Fatal("非连点键不应被识别为连点键")
	}

	// 连点 3 次，第 3 次达标。
	if _, reached := e.registerTapKey(0x1B, true); reached {
		t.Fatal("第 1 次不应达标")
	}
	if _, reached := e.registerTapKey(0x1B, true); reached {
		t.Fatal("第 2 次不应达标")
	}
	isTap, reached := e.registerTapKey(0x1B, true)
	if !isTap || !reached {
		t.Fatalf("第 3 次应达标, isTap=%v reached=%v", isTap, reached)
	}

	// 达标后计数重置，再点一次不应立即达标。
	if _, reached := e.registerTapKey(0x1B, true); reached {
		t.Fatal("重置后第 1 次不应达标")
	}
}

// TestRegisterTapKeyWindow 验证超时窗口外的点击不累计。
func TestRegisterTapKeyWindow(t *testing.T) {
	e := New(Hotkey{})
	e.SetTap(0x1B, 3, 100*time.Millisecond)

	e.registerTapKey(0x1B, true)
	e.registerTapKey(0x1B, true)
	time.Sleep(150 * time.Millisecond) // 超出窗口，前两次作废

	if _, reached := e.registerTapKey(0x1B, true); reached {
		t.Fatal("超时后旧点击应作废，不该达标")
	}
}

// TestRegisterTapKeyUp 验证抬起事件不计数但被识别为连点键。
func TestRegisterTapKeyUp(t *testing.T) {
	e := New(Hotkey{})
	e.SetTap(0x1B, 2, time.Second)
	isTap, reached := e.registerTapKey(0x1B, false) // 抬起
	if !isTap || reached {
		t.Fatalf("抬起应 isTap=true reached=false, 实际 %v %v", isTap, reached)
	}
}
