package hook

import "testing"

// TestKeyName 验证按键名映射。
func TestKeyName(t *testing.T) {
	cases := map[uint32]string{
		'A':  "A",
		'5':  "5",
		0x1B: "ESC",
		0x0D: "回车",
		0x20: "空格",
		0x25: "←",
		0xA3: "右Ctrl",
		0x70: "F1",
		0x60: "小键盘0",
	}
	for vk, want := range cases {
		if got := KeyName(vk); got != want {
			t.Errorf("KeyName(0x%X)=%q, 期望 %q", vk, got, want)
		}
	}
}

// TestButtonName 验证鼠标动作名。
func TestButtonName(t *testing.T) {
	cases := map[MouseButton]string{
		MouseLeft:   "左键",
		MouseRight:  "右键",
		MouseMiddle: "中键",
		MouseWheel:  "滚轮",
	}
	for b, want := range cases {
		if got := ButtonName(b); got != want {
			t.Errorf("ButtonName(%d)=%q, 期望 %q", b, got, want)
		}
	}
}

// TestMouseButton 验证鼠标消息到动作的映射。
func TestMouseButton(t *testing.T) {
	if mouseButton(wmLButtonDown) != MouseLeft {
		t.Error("左键消息映射错误")
	}
	if mouseButton(wmRButtonDown) != MouseRight {
		t.Error("右键消息映射错误")
	}
	if mouseButton(wmMouseMove) != MouseNone {
		t.Error("移动消息不应产生按键")
	}
}

// TestDescribeEvent 验证日志描述文本。
func TestDescribeEvent(t *testing.T) {
	kb := DescribeEvent(Event{Type: EventKeyboard, VK: 'K'})
	if kb != "K" {
		t.Errorf("键盘描述=%q, 期望 K", kb)
	}
	ms := DescribeEvent(Event{Type: EventMouse, Button: MouseRight, X: 100, Y: 200})
	if ms != "右键 @ (100, 200)" {
		t.Errorf("鼠标描述=%q, 期望 右键 @ (100, 200)", ms)
	}
}
