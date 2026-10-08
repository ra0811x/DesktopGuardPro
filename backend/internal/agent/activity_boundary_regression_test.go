package agent

import (
	"context"
	"testing"
	"time"
)

type reviewActivityPolicy struct {
	tick         int
	session      string
	now          time.Time
	directSwitch bool
	sameSession  bool
}

func (p *reviewActivityPolicy) CurrentActivityPolicy(context.Context) (ActivityPolicy, error) {
	p.tick++
	p.session = "A"
	enabled := true
	if p.tick == 3 && !p.directSwitch {
		enabled = false
	}
	if p.tick >= 4 || (p.directSwitch && p.tick >= 3) {
		p.session = "B"
		p.now = p.now.Add(10 * time.Second)
	}
	sessionID := p.session
	revision := uint64(2)
	if p.sameSession {
		sessionID = "A"
		if p.session == "B" {
			revision = 3
		}
	}
	return ActivityPolicy{SessionID: sessionID, SessionRevision: revision, Enabled: enabled, SampleInterval: time.Millisecond, ReportInterval: time.Hour, Capture: defaultActivityCapturePolicy()}, nil
}

type reviewActivitySource struct{ n int }

func (s *reviewActivitySource) Sample(context.Context, ActivityCapturePolicy) (ActivitySample, error) {
	s.n++
	sample := ActivitySample{WindowTitle: "A confidential window", ProcessID: 1, ProcessImage: `C:\A.exe`}
	if s.n == 2 {
		sample.KeyboardActivityCount = 7
	}
	if s.n >= 3 {
		sample.WindowTitle = "B window"
		sample.ProcessID = 2
		sample.ProcessImage = `C:\B.exe`
	}
	return sample, nil
}

type reviewActivityReporter struct {
	policy *reviewActivityPolicy
	cancel context.CancelFunc
	leak   *ActivitySummary
}

func (r *reviewActivityReporter) Report(_ context.Context, summary ActivitySummary) error {
	if r.policy.session == "B" {
		r.leak = &summary
		r.cancel()
	}
	return nil
}
func TestRegressionActivityDoesNotCrossDisabledBoundary(t *testing.T) {
	assertFreshActivityBoundary(t, &reviewActivityPolicy{now: time.Now().UTC()})
}
func TestActivityDoesNotCrossSessionSwitchWithoutDisabledPoll(t *testing.T) {
	assertFreshActivityBoundary(t, &reviewActivityPolicy{now: time.Now().UTC(), directSwitch: true})
}
func TestActivityDoesNotCrossRevisionSwitchWithoutDisabledPoll(t *testing.T) {
	assertFreshActivityBoundary(t, &reviewActivityPolicy{now: time.Now().UTC(), directSwitch: true, sameSession: true})
}
func assertFreshActivityBoundary(t *testing.T, policy *reviewActivityPolicy) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reporter := &reviewActivityReporter{policy: policy, cancel: cancel}
	runtime := newRuntime(RuntimeOptions{Source: &reviewActivitySource{}, Reporter: reporter, PolicyProvider: policy, Now: func() time.Time { return policy.now }})
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if reporter.leak == nil {
		t.Fatal("no report after activity re-enabled")
	}
	if reporter.leak.WindowTitle == "A confidential window" || reporter.leak.KeyboardActivityCount != 0 {
		t.Fatalf("new session receives previous session activity: %+v", *reporter.leak)
	}
	expectedID := policy.session
	expectedRevision := uint64(2)
	if policy.sameSession {
		expectedID = "A"
		expectedRevision = 3
	}
	if reporter.leak.SessionID != expectedID || reporter.leak.SessionRevision != expectedRevision {
		t.Fatalf("wrong activity owner: %+v", *reporter.leak)
	}
}
