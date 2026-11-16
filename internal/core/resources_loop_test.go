package core

import (
	"context"

	"sync"
	"testing"
	"time"
)

// waitFor polls until cond holds, so a test never sleeps for a fixed guess.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// samplingFixture connects a device whose sampling interval is short enough to
// observe ticks, and counts how many rounds ran.
func samplingFixture(t *testing.T) (*managerFixture, func() int) {
	t.Helper()
	f := newManagerFixture(t, func(o *ManagerOptions) { o.ResourceInterval = 10 * time.Millisecond })
	f.connect(t)
	round := defaultRound()
	var mu sync.Mutex
	samples := 0
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		if containsSubstring(c.Args, "@@STAT") {
			mu.Lock()
			samples++
			mu.Unlock()
			return Result{Output: round.String()}, nil
		}
		return Result{}, nil
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return samples
	}
	return f, count
}

func TestResourceSamplingTicksUntilStopped(t *testing.T) {
	f, count := samplingFixture(t)

	f.m.StartResourceSampling(f.id)
	waitFor(t, "two sampling rounds", func() bool { return count() >= 2 })
	f.m.StopResourceSampling(f.id)

	stopped := count()
	time.Sleep(40 * time.Millisecond)
	if after := count(); after != stopped {
		t.Fatalf("sampling kept running after Stop: %d → %d", stopped, after)
	}
}

func TestResourceSamplingStopsOnDisconnect(t *testing.T) {
	f, count := samplingFixture(t)

	f.m.StartResourceSampling(f.id)
	// The counter is bumped inside the fake runner, before the round is
	// parsed and stored; wait for the snapshot itself.
	waitFor(t, "a stored snapshot", func() bool {
		_, ok := f.m.Resources(f.id)
		return ok
	})

	f.m.Disconnect(context.Background(), f.id)
	stopped := count()
	time.Sleep(40 * time.Millisecond)

	if after := count(); after != stopped {
		t.Fatalf("sampling kept running after Disconnect: %d → %d", stopped, after)
	}
	if _, ok := f.m.Resources(f.id); ok {
		t.Fatal("the disconnected device kept its snapshot")
	}
}

func TestStartResourceSamplingIsIdempotent(t *testing.T) {
	f, _ := samplingFixture(t)

	for i := 0; i < 3; i++ {
		f.m.StartResourceSampling(f.id)
	}
	f.m.mu.Lock()
	loops := len(f.m.samplers)
	f.m.mu.Unlock()
	if loops != 1 {
		t.Fatalf("StartResourceSampling started %d loops, want 1", loops)
	}

	f.m.StopResourceSampling(f.id)
	f.m.mu.Lock()
	left := len(f.m.samplers)
	f.m.mu.Unlock()
	if left != 0 {
		t.Fatalf("StopResourceSampling left %d loops", left)
	}
}
