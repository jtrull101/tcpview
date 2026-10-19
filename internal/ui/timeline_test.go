package ui

import (
	"strings"
	"testing"
	"time"
)

// --- positive ---

func TestTimeline_StepBackEntersRewindMode(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	tl.stepBack(now)
	if !tl.active {
		t.Fatal("active = false after stepBack")
	}
	if !tl.at.Equal(now.Add(-rewindStep)) {
		t.Fatalf("at = %v, want %v", tl.at, now.Add(-rewindStep))
	}
}

func TestTimeline_RepeatedStepBackMovesFurtherBack(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	tl.stepBack(now)
	first := tl.at
	tl.stepBack(now)
	if !tl.at.Equal(first.Add(-rewindStep)) {
		t.Fatalf("second stepBack: at = %v, want %v", tl.at, first.Add(-rewindStep))
	}
}

func TestTimeline_Window_IsCenteredOnAt(t *testing.T) {
	t.Parallel()
	tl := timelineState{active: true, at: time.Unix(1000, 0)}
	from, to := tl.window()
	if !from.Equal(tl.at.Add(-rewindWindow)) || !to.Equal(tl.at.Add(rewindWindow)) {
		t.Fatalf("window = [%v, %v], want centered on %v +/- %v", from, to, tl.at, rewindWindow)
	}
}

func TestTimeline_ViewMentionsRewoundTime(t *testing.T) {
	t.Parallel()
	tl := timelineState{active: true, at: time.Date(2026, 1, 1, 15, 4, 5, 0, time.UTC)}
	if out := tl.view(); !strings.Contains(out, "15:04:05") {
		t.Fatalf("view() = %q, want it to mention the rewound time", out)
	}
}

// --- negative ---

func TestTimeline_StepForwardWhileNotActiveIsNoop(t *testing.T) {
	t.Parallel()
	var tl timelineState
	tl.stepForward(time.Now())
	if tl.active {
		t.Fatal("active = true after stepForward on an inactive timeline")
	}
}

func TestTimeline_StepForwardPastNowExitsToLive(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	tl.stepBack(now) // at = now - 10s
	tl.stepForward(now)
	if tl.active {
		t.Fatal("active = true after stepping forward back to the live edge")
	}
}

func TestTimeline_JumpToLiveResetsState(t *testing.T) {
	t.Parallel()
	tl := timelineState{active: true, at: time.Now()}
	tl.jumpToLive()
	if tl.active || !tl.at.IsZero() {
		t.Fatalf("jumpToLive left state = %+v, want zero", tl)
	}
}

// --- load ---

func TestTimeline_Load_ManyStepsStayConsistent(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	for i := 0; i < 10_000; i++ {
		tl.stepBack(now)
	}
	want := now.Add(-time.Duration(10_000) * rewindStep)
	if !tl.at.Equal(want) {
		t.Fatalf("at = %v after 10000 steps back, want %v", tl.at, want)
	}
}

// --- stress ---

func TestTimeline_Stress_AlternatingStepsConverge(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	for i := 0; i < 500; i++ {
		tl.stepBack(now)
	}
	for i := 0; i < 500; i++ {
		tl.stepForward(now)
	}
	if tl.active {
		t.Fatal("active = true after stepping forward exactly as many times as back")
	}
}

// --- chaos ---

func TestTimeline_Chaos_RandomStepSequencesNeverPanic(t *testing.T) {
	t.Parallel()
	var tl timelineState
	now := time.Now()
	ops := []func(){
		func() { tl.stepBack(now) },
		func() { tl.stepForward(now) },
		func() { tl.jumpToLive() },
	}
	for i := 0; i < 2000; i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("op panicked at i=%d: %v", i, rec)
				}
			}()
			ops[i%len(ops)]()
			_ = tl.view()
			from, to := tl.window()
			if from.After(to) {
				t.Fatalf("window inverted at i=%d: from=%v after to=%v", i, from, to)
			}
		}()
	}
}
