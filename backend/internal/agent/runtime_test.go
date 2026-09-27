package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestActivityCapturePolicySuspendsInputCountsWhileInputShieldIsEnabled(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	capture := activityCapturePolicyForMonitoringPolicy(policy)
	if !capture.RecordForegroundApplication || capture.RecordKeyboardActivity || capture.RecordMouseClicks ||
		capture.RecordMouseWheel || capture.RecordHighRiskShortcuts {
		t.Fatalf("activity capture during input shield = %+v", capture)
	}
}

func TestTemporaryInputControlSuspendsInputCountsWithoutDisablingForegroundAudit(t *testing.T) {
	capture := ActivityCapturePolicy{
		RecordForegroundApplication: true,
		RecordWindowTitle:           true,
		RecordKeyboardActivity:      true,
		RecordMouseClicks:           true,
		RecordMouseWheel:            true,
		RecordHighRiskShortcuts:     true,
	}
	capture = activityCapturePolicyForInputControl(capture, true)
	if !capture.RecordForegroundApplication || !capture.RecordWindowTitle || capture.RecordKeyboardActivity ||
		capture.RecordMouseClicks || capture.RecordMouseWheel || capture.RecordHighRiskShortcuts {
		t.Fatalf("activity capture during temporary input control = %+v", capture)
	}
}

func TestRuntimeStopsAllComponentsWhenInputShieldFails(t *testing.T) {
	want := errors.New("input shield failed")
	component := &testRuntimeComponent{result: want, stopped: make(chan struct{})}
	runtime := newRuntime(RuntimeOptions{InputShield: component, SampleInterval: time.Hour})
	if err := runtime.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	select {
	case <-component.stopped:
	default:
		t.Fatal("input shield component did not finish")
	}
}

func TestRuntimeStopsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	runtime := NewRuntime()
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestRuntimeRefreshesSessionActivityIntervals(t *testing.T) {
	runtime := newRuntime(RuntimeOptions{
		SampleInterval: time.Second,
		ReportInterval: time.Minute,
		PolicyProvider: staticActivityPolicyProvider{policy: ActivityPolicy{
			Enabled: true, SampleInterval: 7 * time.Second, ReportInterval: 45 * time.Second,
			Capture: ActivityCapturePolicy{RecordKeyboardActivity: true},
		}},
	})
	runtime.refreshActivityPolicy(context.Background())
	if !runtime.enabled || runtime.sampleInterval != 7*time.Second || runtime.reportInterval != 45*time.Second ||
		!runtime.capture.RecordKeyboardActivity {
		t.Fatalf("runtime policy = enabled:%t sample:%s report:%s", runtime.enabled, runtime.sampleInterval, runtime.reportInterval)
	}
}

func TestApplyActivityCapturePolicyRemovesDisabledDetails(t *testing.T) {
	sample := ActivitySample{
		WindowTitle: "敏感标题", ProcessID: 42, ProcessImage: `C:\Apps\example.exe`, LastInputTick: 10,
		KeyboardActivityCount: 3, MouseClickCount: 2, MouseWheelCount: 1,
		HighRiskShortcutCount: map[string]uint64{"alt_tab": 1},
	}
	filtered := applyActivityCapturePolicy(sample, ActivityCapturePolicy{RecordKeyboardActivity: true})
	if filtered.WindowTitle != "" || filtered.ProcessID != 0 || filtered.ProcessImage != "" ||
		filtered.MouseClickCount != 0 || filtered.MouseWheelCount != 0 || filtered.HighRiskShortcutCount != nil ||
		filtered.KeyboardActivityCount != 3 {
		t.Fatalf("filtered activity sample = %+v", filtered)
	}
}

func TestRuntimeReportsWindowChangesAndAggregatesInputActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testActivitySource{samples: []ActivitySample{
		{WindowTitle: "工作簿 - 示例", ProcessID: 101, ProcessImage: `C:\Apps\sheet.exe`, LastInputTick: 100},
		{WindowTitle: "工作簿 - 示例", ProcessID: 101, ProcessImage: `C:\Apps\sheet.exe`, LastInputTick: 101, KeyboardActivityCount: 2, MouseClickCount: 1, HighRiskShortcutCount: map[string]uint64{"alt_tab": 1}},
		{WindowTitle: "报告 - 示例", ProcessID: 102, ProcessImage: `C:\Apps\report.exe`, LastInputTick: 102, KeyboardActivityCount: 1, MouseWheelCount: 1},
	}}
	reporter := &testActivityReporter{cancel: cancel}
	baseUTC := time.Date(2026, time.September, 13, 1, 2, 3, 0, time.UTC)
	nowIndex := 0
	runtime := newRuntime(RuntimeOptions{
		Source: source, Reporter: reporter, SampleInterval: time.Millisecond, ReportInterval: time.Hour,
		Now: func() time.Time {
			value := baseUTC.Add(time.Duration(nowIndex) * time.Second)
			nowIndex++
			return value
		},
	})
	if err := runtime.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(reporter.summaries) != 2 {
		t.Fatalf("reported summaries = %#v, want two window summaries", reporter.summaries)
	}
	if first := reporter.summaries[0]; first.WindowTitle != "工作簿 - 示例" || first.InputActivityCount != 0 {
		t.Fatalf("first summary = %#v", first)
	}
	if second := reporter.summaries[1]; second.WindowTitle != "工作簿 - 示例" || second.InputActivityCount != 3 ||
		second.KeyboardActivityCount != 2 || second.MouseClickCount != 1 || second.MouseWheelCount != 0 ||
		second.HighRiskShortcutCount["alt_tab"] != 1 ||
		second.ActivityStartUTC.IsZero() || second.ActivityEndUTC.IsZero() ||
		!second.ForegroundStartUTC.Equal(baseUTC) || !second.ForegroundEndUTC.Equal(baseUTC.Add(2*time.Second)) ||
		second.ForegroundDurationMS != 2000 {
		t.Fatalf("second summary = %#v, want input metadata attributed to the previous foreground window", second)
	}
	for index := 0; index < reflect.TypeOf(ActivitySummary{}).NumField(); index++ {
		field := reflect.TypeOf(ActivitySummary{}).Field(index)
		if field.Name == "KeyboardText" || field.Name == "KeyText" || field.Name == "KeyStroke" {
			t.Fatalf("activity summary must not contain keyboard text field %q", field.Name)
		}
	}
}

func TestHighRiskShortcutSamplingKeepsOnlyExplicitCategories(t *testing.T) {
	previous := make(map[string]bool)
	keys := [256]bool{}
	keys[0x12], keys[0x09] = true, true
	first := sampleHighRiskShortcutTransitions(previous, keys)
	second := sampleHighRiskShortcutTransitions(previous, keys)
	keys[0x09] = false
	sampleHighRiskShortcutTransitions(previous, keys)
	keys[0x09] = true
	third := sampleHighRiskShortcutTransitions(previous, keys)
	if first["alt_tab"] != 1 || len(second) != 0 || third["alt_tab"] != 1 {
		t.Fatalf("shortcut transitions first=%v second=%v third=%v", first, second, third)
	}
	for category := range first {
		if category != "alt_tab" {
			t.Fatalf("unexpected shortcut category %q", category)
		}
	}
}

func TestSampleInputTransitionsCountsKeyboardAndMouseWithoutKeepingKeyIdentity(t *testing.T) {
	previous := [256]bool{}
	states := map[int]struct {
		down    bool
		pressed bool
	}{
		0x01: {pressed: true},
		0x41: {down: true},
		0x42: {pressed: true},
	}
	keyboard, mouse := sampleInputTransitions(&previous, func(virtualKey int) (bool, bool) {
		state := states[virtualKey]
		return state.down, state.pressed
	})
	if keyboard != 2 || mouse != 1 || !previous[0x41] {
		t.Fatalf("input counters keyboard=%d mouse=%d key-down=%t", keyboard, mouse, previous[0x41])
	}
}

func TestMouseWheelMessageClassificationIncludesVerticalAndHorizontalScrolling(t *testing.T) {
	if !isMouseWheelMessage(windowsMessageMouseWheel) || !isMouseWheelMessage(windowsMessageMouseHWheel) {
		t.Fatal("mouse wheel messages were not classified as scroll activity")
	}
	if isMouseWheelMessage(0x0201) {
		t.Fatal("mouse click was classified as scroll activity")
	}
}

type testActivitySource struct {
	samples []ActivitySample
	index   int
}

type testRuntimeComponent struct {
	result  error
	stopped chan struct{}
}

func (component *testRuntimeComponent) Run(context.Context) error {
	close(component.stopped)
	return component.result
}

type staticActivityPolicyProvider struct {
	policy ActivityPolicy
}

func (provider staticActivityPolicyProvider) CurrentActivityPolicy(context.Context) (ActivityPolicy, error) {
	return provider.policy, nil
}

func (source *testActivitySource) Sample(context.Context, ActivityCapturePolicy) (ActivitySample, error) {
	if source.index >= len(source.samples) {
		return source.samples[len(source.samples)-1], nil
	}
	sample := source.samples[source.index]
	source.index++
	return sample, nil
}

type testActivityReporter struct {
	summaries []ActivitySummary
	cancel    context.CancelFunc
}

func (reporter *testActivityReporter) Report(_ context.Context, summary ActivitySummary) error {
	reporter.summaries = append(reporter.summaries, summary)
	if len(reporter.summaries) == 2 {
		reporter.cancel()
	}
	return nil
}
