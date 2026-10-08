package agent

import (
	"context"
	"time"
)

const (
	defaultSampleInterval = 5 * time.Second
	defaultReportInterval = 30 * time.Second
)

type ActivitySample struct {
	WindowTitle           string
	ProcessID             uint32
	ProcessImage          string
	LastInputTick         uint32
	KeyboardActivityCount uint64
	MouseClickCount       uint64
	MouseWheelCount       uint64
	HighRiskShortcutCount map[string]uint64
}

type ActivitySummary struct {
	SessionID             string
	SessionRevision       uint64
	WindowTitle           string
	ProcessID             uint32
	ProcessImage          string
	InputActivityCount    uint64
	KeyboardActivityCount uint64
	MouseClickCount       uint64
	MouseWheelCount       uint64
	HighRiskShortcutCount map[string]uint64
	ActivityStartUTC      time.Time
	ActivityEndUTC        time.Time
	ForegroundStartUTC    time.Time
	ForegroundEndUTC      time.Time
	ForegroundDurationMS  int64
	ObservedUTC           time.Time
}

type ActivitySource interface {
	Sample(context.Context, ActivityCapturePolicy) (ActivitySample, error)
}

type ActivityReporter interface {
	Report(context.Context, ActivitySummary) error
}

type ActivityPolicy struct {
	SessionID       string
	SessionRevision uint64
	Enabled         bool
	SampleInterval  time.Duration
	ReportInterval  time.Duration
	Capture         ActivityCapturePolicy
}

type ActivityCapturePolicy struct {
	RecordForegroundApplication bool
	RecordWindowTitle           bool
	RecordKeyboardActivity      bool
	RecordMouseClicks           bool
	RecordMouseWheel            bool
	RecordHighRiskShortcuts     bool
}

type ActivityPolicyProvider interface {
	CurrentActivityPolicy(context.Context) (ActivityPolicy, error)
}

type RuntimeComponent interface {
	Run(context.Context) error
}

type RuntimeOptions struct {
	Source         ActivitySource
	Reporter       ActivityReporter
	PolicyProvider ActivityPolicyProvider
	InputShield    RuntimeComponent
	SampleInterval time.Duration
	ReportInterval time.Duration
	Now            func() time.Time
}

type Runtime struct {
	source          ActivitySource
	reporter        ActivityReporter
	policyProvider  ActivityPolicyProvider
	inputShield     RuntimeComponent
	enabled         bool
	sessionID       string
	sessionRevision uint64
	sampleInterval  time.Duration
	reportInterval  time.Duration
	capture         ActivityCapturePolicy
	now             func() time.Time
}

func NewRuntime() *Runtime {
	reporter := newPipeActivityReporter()
	return newRuntime(RuntimeOptions{
		Source: newWindowsActivitySource(), Reporter: reporter, PolicyProvider: reporter,
		InputShield: newWindowsInputShieldRuntime(),
	})
}

func newRuntime(options RuntimeOptions) *Runtime {
	if options.SampleInterval <= 0 {
		options.SampleInterval = defaultSampleInterval
	}
	if options.ReportInterval <= 0 {
		options.ReportInterval = defaultReportInterval
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Runtime{
		source: options.Source, reporter: options.Reporter, policyProvider: options.PolicyProvider, enabled: true,
		inputShield:    options.InputShield,
		sampleInterval: options.SampleInterval, capture: defaultActivityCapturePolicy(),
		reportInterval: options.ReportInterval, now: options.Now,
	}
}

func defaultActivityCapturePolicy() ActivityCapturePolicy {
	return ActivityCapturePolicy{
		RecordForegroundApplication: true,
		RecordWindowTitle:           true,
		RecordKeyboardActivity:      true,
		RecordMouseClicks:           true,
		RecordMouseWheel:            true,
		RecordHighRiskShortcuts:     true,
	}
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return nil
	}
	if runtime.inputShield == nil {
		return runtime.runActivity(ctx)
	}
	componentContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- runtime.runActivity(componentContext) }()
	go func() { results <- runtime.inputShield.Run(componentContext) }()
	first := <-results
	cancel()
	second := <-results
	if first != nil {
		return first
	}
	return second
}

func (runtime *Runtime) runActivity(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	state := activityState{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if runtime.refreshActivityPolicy(ctx) {
				state = activityState{}
				if resetter, ok := runtime.source.(interface{ ResetActivityCapture() }); ok {
					resetter.ResetActivityCapture()
				}
			}
			if runtime.enabled && runtime.source != nil && runtime.reporter != nil {
				runtime.sampleAndReport(ctx, &state)
			}
			timer.Reset(runtime.sampleInterval)
		}
	}
}

func (runtime *Runtime) refreshActivityPolicy(ctx context.Context) bool {
	if runtime.policyProvider == nil {
		return false
	}
	policy, err := runtime.policyProvider.CurrentActivityPolicy(ctx)
	if err != nil {
		changed := runtime.enabled
		runtime.enabled = false
		return changed
	}
	changed := runtime.enabled != policy.Enabled || runtime.sessionID != policy.SessionID ||
		runtime.sessionRevision != policy.SessionRevision || runtime.capture != policy.Capture
	runtime.enabled = policy.Enabled
	runtime.sessionID = policy.SessionID
	runtime.sessionRevision = policy.SessionRevision
	if policy.SampleInterval > 0 {
		runtime.sampleInterval = policy.SampleInterval
	}
	if policy.ReportInterval > 0 {
		runtime.reportInterval = policy.ReportInterval
	}
	runtime.capture = policy.Capture
	return changed
}

type activityState struct {
	sessionID             string
	sessionRevision       uint64
	hasInputTick          bool
	lastInputTick         uint32
	inputActivityCount    uint64
	keyboardActivityCount uint64
	mouseClickCount       uint64
	mouseWheelCount       uint64
	highRiskShortcutCount map[string]uint64
	activityStartUTC      time.Time
	activityEndUTC        time.Time
	lastWindowTitle       string
	lastProcessID         uint32
	lastProcessImage      string
	foregroundStartUTC    time.Time
	lastReportedUTC       time.Time
}

func (runtime *Runtime) sampleAndReport(ctx context.Context, state *activityState) {
	sample, err := runtime.source.Sample(ctx, runtime.capture)
	if err != nil {
		return
	}
	sample = applyActivityCapturePolicy(sample, runtime.capture)
	now := runtime.now().UTC()
	firstSample := state.foregroundStartUTC.IsZero()
	windowChanged := !firstSample && (state.lastWindowTitle != sample.WindowTitle ||
		state.lastProcessID != sample.ProcessID || state.lastProcessImage != sample.ProcessImage)
	if windowChanged {
		if runtime.reportCurrentWindow(ctx, state, now) != nil {
			return
		}
		runtime.resetInputActivity(state)
		state.lastWindowTitle = sample.WindowTitle
		state.lastProcessID = sample.ProcessID
		state.lastProcessImage = sample.ProcessImage
		state.foregroundStartUTC = now
		state.lastReportedUTC = now
	} else if firstSample {
		state.sessionID = runtime.sessionID
		state.sessionRevision = runtime.sessionRevision
		state.lastWindowTitle = sample.WindowTitle
		state.lastProcessID = sample.ProcessID
		state.lastProcessImage = sample.ProcessImage
		state.foregroundStartUTC = now
	}
	inputDelta := sample.KeyboardActivityCount + sample.MouseClickCount + sample.MouseWheelCount
	if inputDelta > 0 {
		if state.activityStartUTC.IsZero() {
			state.activityStartUTC = now
		}
		state.activityEndUTC = now
		state.inputActivityCount += inputDelta
		state.keyboardActivityCount += sample.KeyboardActivityCount
		state.mouseClickCount += sample.MouseClickCount
		state.mouseWheelCount += sample.MouseWheelCount
		if len(sample.HighRiskShortcutCount) > 0 && state.highRiskShortcutCount == nil {
			state.highRiskShortcutCount = make(map[string]uint64)
		}
		for category, count := range sample.HighRiskShortcutCount {
			state.highRiskShortcutCount[category] += count
		}
	}
	state.hasInputTick = true
	state.lastInputTick = sample.LastInputTick
	if windowChanged {
		return
	}
	reportDue := state.lastReportedUTC.IsZero() || now.Sub(state.lastReportedUTC) >= runtime.reportInterval
	if !firstSample && (state.inputActivityCount == 0 || !reportDue) {
		return
	}
	if runtime.reportCurrentWindow(ctx, state, now) != nil {
		return
	}
	runtime.resetInputActivity(state)
	state.lastReportedUTC = now
}

func applyActivityCapturePolicy(sample ActivitySample, policy ActivityCapturePolicy) ActivitySample {
	if !policy.RecordForegroundApplication {
		sample.ProcessID = 0
		sample.ProcessImage = ""
	}
	if !policy.RecordWindowTitle {
		sample.WindowTitle = ""
	}
	if !policy.RecordKeyboardActivity {
		sample.KeyboardActivityCount = 0
	}
	if !policy.RecordMouseClicks {
		sample.MouseClickCount = 0
	}
	if !policy.RecordMouseWheel {
		sample.MouseWheelCount = 0
	}
	if !policy.RecordHighRiskShortcuts {
		sample.HighRiskShortcutCount = nil
	}
	return sample
}

func (runtime *Runtime) reportCurrentWindow(ctx context.Context, state *activityState, now time.Time) error {
	summary := ActivitySummary{
		SessionID: state.sessionID, SessionRevision: state.sessionRevision,
		WindowTitle: state.lastWindowTitle, ProcessID: state.lastProcessID, ProcessImage: state.lastProcessImage,
		InputActivityCount: state.inputActivityCount, KeyboardActivityCount: state.keyboardActivityCount,
		MouseClickCount: state.mouseClickCount, MouseWheelCount: state.mouseWheelCount,
		HighRiskShortcutCount: cloneShortcutCounts(state.highRiskShortcutCount),
		ActivityStartUTC:      state.activityStartUTC, ActivityEndUTC: state.activityEndUTC, ObservedUTC: now,
		ForegroundStartUTC: state.foregroundStartUTC, ForegroundEndUTC: now,
		ForegroundDurationMS: now.Sub(state.foregroundStartUTC).Milliseconds(),
	}
	return runtime.reporter.Report(ctx, summary)
}

func (runtime *Runtime) resetInputActivity(state *activityState) {
	state.inputActivityCount = 0
	state.keyboardActivityCount = 0
	state.mouseClickCount = 0
	state.mouseWheelCount = 0
	state.highRiskShortcutCount = nil
	state.activityStartUTC = time.Time{}
	state.activityEndUTC = time.Time{}
}

func cloneShortcutCounts(source map[string]uint64) map[string]uint64 {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]uint64, len(source))
	for category, count := range source {
		result[category] = count
	}
	return result
}
