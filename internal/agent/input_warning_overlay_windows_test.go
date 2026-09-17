package agent

import (
	"reflect"
	"testing"
)

func TestInputWarningOverlayLayoutsCoverEveryDisplay(t *testing.T) {
	monitors := []inputOverlayRect{
		{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
		{Left: -1280, Top: -200, Right: 0, Bottom: 824},
		{Left: 1920, Top: 0, Right: 5760, Bottom: 2160},
		{Left: 0, Top: 0, Right: 0, Bottom: 100},
	}
	want := []inputOverlayRect{
		{Left: 0, Top: 463, Right: 1920, Bottom: 617},
		{Left: -1280, Top: 239, Right: 0, Bottom: 385},
		{Left: 1920, Top: 1000, Right: 5760, Bottom: 1160},
	}
	if got := inputWarningOverlayLayouts(monitors); !reflect.DeepEqual(got, want) {
		t.Fatalf("inputWarningOverlayLayouts() = %+v, want %+v", got, want)
	}
}
