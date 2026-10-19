package ui

import (
	"fmt"
	"time"
)

// rewindStep is how far one scrub key press moves the viewed point in time.
const rewindStep = 10 * time.Second

// rewindWindow is the +/- window of traffic shown around the rewind point -
// like scrubbing a video to a timestamp and seeing what's playing around
// it, rather than a single instant.
const rewindWindow = 5 * time.Second

// timelineState tracks rewind mode: whether the user is viewing historical
// traffic around a point in time instead of the live tail, and that point.
type timelineState struct {
	active bool
	at     time.Time
}

// stepBack enters rewind mode (if not already active, starting one step
// before now) or moves further back in time (if already rewound).
func (t *timelineState) stepBack(now time.Time) {
	if !t.active {
		t.active = true
		t.at = now.Add(-rewindStep)
		return
	}
	t.at = t.at.Add(-rewindStep)
}

// stepForward moves the rewind point forward; stepping forward past "now"
// exits rewind mode back to the live tail, same as scrubbing a video to
// its live edge.
func (t *timelineState) stepForward(now time.Time) {
	if !t.active {
		return
	}
	t.at = t.at.Add(rewindStep)
	if !t.at.Before(now) {
		t.jumpToLive()
	}
}

func (t *timelineState) jumpToLive() {
	t.active = false
	t.at = time.Time{}
}

// window returns the [from, to] range of traffic to display for the
// current rewind point.
func (t timelineState) window() (time.Time, time.Time) {
	return t.at.Add(-rewindWindow), t.at.Add(rewindWindow)
}

func (t timelineState) view() string {
	if !t.active {
		return "←/→ rewind  "
	}
	return fmt.Sprintf("REWOUND to %s  ←/→ scrub  L live  ", t.at.Format("15:04:05"))
}
