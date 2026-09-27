package agent

import "testing"

func TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable(t *testing.T) {
	releaseFirst, acquired, err := AcquireInteractiveAgentInstance()
	if err != nil || !acquired {
		t.Fatalf("first acquire = acquired:%t error:%v", acquired, err)
	}
	defer releaseFirst()

	releaseSecond, acquired, err := AcquireInteractiveAgentInstance()
	if err != nil || acquired {
		t.Fatalf("second acquire = acquired:%t error:%v", acquired, err)
	}
	releaseSecond()

	releaseFirst()
	releaseThird, acquired, err := AcquireInteractiveAgentInstance()
	if err != nil || !acquired {
		t.Fatalf("third acquire after release = acquired:%t error:%v", acquired, err)
	}
	releaseThird()
}
