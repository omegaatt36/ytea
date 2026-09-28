package spectrum

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestMeterPublishesPushedAudioThenFalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMeter(16, rate)
		go m.Run(t.Context())

		m.Push(sine(1000, 1))
		time.Sleep(time.Second / frameRate)
		loud := <-m.Levels()
		if slices.Max(loud) < 0.9 {
			t.Fatalf("levels after a full-scale sine peak at %v, want near 1", slices.Max(loud))
		}

		// Without new samples the meter analyzes silence, so bars decay.
		time.Sleep(time.Second)
		quiet := <-m.Levels()
		if slices.Max(quiet) >= slices.Max(loud)/2 {
			t.Errorf("levels after 1s without samples peak at %v, want below %v", slices.Max(quiet), slices.Max(loud)/2)
		}
	})
}

func TestMeterPushKeepsLatestWindow(t *testing.T) {
	m := NewMeter(16, rate)
	m.Push([]float64{1, 2})
	m.Push(slices.Repeat([]float64{3}, Size+5))
	if m.ring[0] != 3 || m.ring[Size-1] != 3 {
		t.Errorf("ring ends = %v, %v after oversized push, want 3, 3", m.ring[0], m.ring[Size-1])
	}
}
