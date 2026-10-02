package service

import (
	"slices"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

func updateCore(t *testing.T, c *Core, msg any) []Cmd {
	t.Helper()
	if msg == nil {
		return c.Update(nil).Cmds
	}
	coreMsg, ok := msg.(Msg)
	if !ok {
		t.Fatalf("%T cannot be fed into the reducer", msg)
	}
	return c.Update(coreMsg).Cmds
}

func runCore(t *testing.T, c *Core, cmds []Cmd) {
	t.Helper()
	for len(cmds) > 0 {
		cmd := cmds[0]
		cmds = cmds[1:]
		if cmd != nil {
			cmds = append(cmds, updateCore(t, c, cmd())...)
		}
	}
}

func TestUpdateDrivesSearchAndDropsStaleResults(t *testing.T) {
	track := watch("one")
	c := New(Deps{Searcher: searchStub{tracks: []domain.Track{track}}})
	old, _ := c.Submit("old")
	current, _ := c.Submit("current")
	runCore(t, &c, updateCore(t, &c, old()))
	if !c.Busy() || len(c.Search.Tracks) != 0 {
		t.Fatal("stale search changed the state")
	}
	runCore(t, &c, updateCore(t, &c, current()))
	if c.Busy() || c.Search.Query != "current" || !slices.Equal(c.Search.Tracks, []domain.Track{track}) {
		t.Fatalf("search = %+v, busy = %v", c.Search, c.Busy())
	}
}

func TestUpdateDrivesImportUntilAppendCompletion(t *testing.T) {
	track := watch("one")
	writes := make(chan string, 1)
	c := New(Deps{Searcher: searchStub{tracks: []domain.Track{track}}, Player: appendRecorder{writes: writes}})
	lookup, _ := c.Submit(track.URL)
	runCore(t, &c, []Cmd{lookup})
	if c.Busy() || c.Tracks[track.URL] != track {
		t.Fatalf("import metadata = %+v, busy = %v", c.Tracks, c.Busy())
	}
	select {
	case got := <-writes:
		if got != track.URL {
			t.Fatalf("append = %q", got)
		}
	default:
		t.Fatal("lookup did not append")
	}
}

func TestUpdateDrivesQueueActionAndRefresh(t *testing.T) {
	p := &fakePlayer{}
	c := New(Deps{Player: p})
	c.Queue.Entries = entries("one")
	runCore(t, &c, []Cmd{c.ClearQueue()})
	if c.Queue.Projected() || c.Queue.Pos != -1 || len(c.Queue.Entries) != 0 || !slices.Equal(p.calls, []string{"stop"}) {
		t.Fatalf("queue = %+v, calls = %v", c.Queue, p.calls)
	}
}

func TestUpdateRecordsAudiblePlayerEventsOnce(t *testing.T) {
	history := &fakeHistory{}
	c := New(Deps{History: history})
	c.Tracks["one"] = domain.Track{URL: "one", Title: "Song"}
	for _, ev := range []PlayerEvent{QueueChanged{Entries: entries("one")}, QueuePosChanged{Pos: 0}, IdleChanged{Idle: false}, DurationChanged{Duration: time.Minute}, PositionChanged{Pos: time.Second}, PositionChanged{Pos: 2 * time.Second}} {
		runCore(t, &c, updateCore(t, &c, ev))
	}
	if len(history.recorded) != 1 || history.recorded[0].Duration != time.Minute {
		t.Fatalf("recorded = %+v", history.recorded)
	}
}

func TestUpdateAppliesAccountAndDeviceResults(t *testing.T) {
	c := New(Deps{Account: fakeAccount{playlists: []domain.AccountPlaylist{{ID: "p"}}}})
	runCore(t, &c, []Cmd{c.Account.Reload()})
	if c.Account.Loading || len(c.Account.Playlists) != 1 {
		t.Fatalf("account = %+v", c.Account)
	}
	devices := []domain.AudioDevice{{Name: "device"}}
	updateCore(t, &c, DevicesLoaded{Devices: devices})
	if !slices.Equal(c.Devices, devices) {
		t.Fatalf("devices = %+v", c.Devices)
	}
}

func TestUpdateReportsSearchAndTrackTransitions(t *testing.T) {
	c := New(Deps{Searcher: searchStub{tracks: []domain.Track{watch("one")}}})
	old, _ := c.Submit("old")
	latest, _ := c.Submit("new")
	if result := c.Update(old()); result.SearchAccepted || result.SearchAdded != 0 {
		t.Fatalf("stale result = %+v", result)
	}
	if result := c.Update(latest()); !result.SearchAccepted || result.SearchAdded != 1 {
		t.Fatalf("latest result = %+v", result)
	}
	if result := c.Update(QueuePosChanged{Pos: 0}); !result.SwitchesTrack {
		t.Fatal("queue position did not report track transition")
	}
	if result := c.Update(VolumeChanged{Volume: 50}); result.SwitchesTrack {
		t.Fatal("volume reported track transition")
	}
}

func TestUpdateKeepsLoadedStateWhenDeviceOrStreamRequestFails(t *testing.T) {
	c := New(Deps{})
	devices := []domain.AudioDevice{{Name: "device"}}
	c.Update(DevicesLoaded{Devices: devices})
	info := domain.StreamInfo{Path: "one", Codec: "opus"}
	c.Update(StreamLoaded{Info: info})
	c.Update(DevicesLoaded{Err: ErrEditsPending})
	c.Update(StreamLoaded{Err: ErrEditsPending})
	if !slices.Equal(c.Devices, devices) || c.Stream != info {
		t.Fatalf("devices = %+v, stream = %+v", c.Devices, c.Stream)
	}
	if len(c.Update(nil).Cmds) != 0 || len(c.Update(Failed{Err: ErrEditsPending}).Cmds) != 0 {
		t.Fatal("presentation-only results produced work")
	}
}
