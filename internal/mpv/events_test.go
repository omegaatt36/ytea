package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
	"github.com/omegaatt36/ytea/service"
)

func TestTranslate(t *testing.T) {
	prop := func(name, data string) Event {
		return Event{Name: "property-change", Prop: name, Data: json.RawMessage(data)}
	}
	tests := []struct {
		name   string
		ev     Event
		want   service.PlayerEvent
		wanted bool
	}{
		{name: "time-pos", ev: prop(PropTimePos, "12.5"), want: service.PositionChanged{Pos: 12500 * time.Millisecond}, wanted: true},
		{name: "time-pos unavailable", ev: prop(PropTimePos, "null"), want: service.PositionChanged{}, wanted: true},
		{name: "duration", ev: prop(PropDuration, "200"), want: service.DurationChanged{Duration: 200 * time.Second}, wanted: true},
		{name: "pause", ev: prop(PropPause, "true"), want: service.PauseChanged{Paused: true}, wanted: true},
		{name: "idle", ev: prop(PropIdle, "true"), want: service.IdleChanged{Idle: true}, wanted: true},
		{name: "volume", ev: prop(PropVolume, "55"), want: service.VolumeChanged{Volume: 55}, wanted: true},
		{name: "codec", ev: prop(PropCodec, `"opus"`), want: service.CodecChanged{Codec: "opus"}, wanted: true},
		{
			name: "audio params", ev: prop(PropAudioParams, `{"format":"floatp","samplerate":48000,"channels":"stereo"}`),
			want:   service.ParamsChanged{Params: domain.AudioParams{Format: "floatp", SampleRate: 48000, Channels: "stereo"}},
			wanted: true,
		},
		{name: "audio device", ev: prop(PropAudioDevice, `"pipewire/DX5"`), want: service.DeviceChanged{Device: "pipewire/DX5"}, wanted: true},
		{name: "normalize on", ev: prop(PropAF, `[{"label":"norm","name":"lavfi"}]`), want: service.NormalizeChanged{On: true}, wanted: true},
		{name: "normalize off", ev: prop(PropAF, `[]`), want: service.NormalizeChanged{}, wanted: true},
		{name: "other filter", ev: prop(PropAF, `[{"label":"x","name":"lavfi"}]`), want: service.NormalizeChanged{}, wanted: true},
		{name: "loop-playlist", ev: prop(PropLoopPlaylist, `"inf"`), want: service.LoopPlaylistChanged{On: true}, wanted: true},
		{name: "loop-file", ev: prop(PropLoopFile, `"no"`), want: service.LoopFileChanged{}, wanted: true},
		{
			name: "playlist", ev: prop(PropPlaylist, `[{"id":1,"filename":"A","title":"a","current":true,"playing":true},{"id":2,"filename":"B"}]`),
			want: service.QueueChanged{Entries: []domain.PlaylistEntry{
				{Filename: "A", Title: "a", Current: true, Playing: true},
				{Filename: "B"},
			}},
			wanted: true,
		},
		{name: "playlist unavailable", ev: prop(PropPlaylist, "null"), want: service.QueueChanged{}, wanted: true},
		{name: "playlist-pos", ev: prop(PropPlaylistPos, "3"), want: service.QueuePosChanged{Pos: 3}, wanted: true},
		{name: "playlist-pos unavailable", ev: prop(PropPlaylistPos, "null"), want: service.QueuePosChanged{Pos: -1}, wanted: true},
		{name: "playback-restart", ev: Event{Name: "playback-restart"}, want: service.PlaybackRestarted{}, wanted: true},
		{name: "file-loaded", ev: Event{Name: "file-loaded"}, want: service.FileLoaded{}, wanted: true},
		{name: "end-file error", ev: Event{Name: "end-file", Reason: "error", FileError: "unavailable"}, want: service.PlaybackFailed{Reason: "unavailable"}, wanted: true},
		{name: "end-file eof", ev: Event{Name: "end-file", Reason: "eof"}},
		{name: "unmodelled property", ev: prop("mute", "true")},
		{name: "ipc-error", ev: Event{Name: "ipc-error", Reason: "boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := translate(tt.ev)
			if ok != tt.wanted {
				t.Fatalf("translate() ok = %v, want %v", ok, tt.wanted)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("translate() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPlayerEventsTranslatesAndDrops(t *testing.T) {
	c := newFakePair(t, func(req request) string {
		return `{"event":"property-change","name":"pause","data":true}` + "\n" +
			`{"event":"tracks-changed"}` + "\n" +
			`{"event":"end-file","reason":"eof"}` + "\n" +
			`{"event":"file-loaded"}` + "\n" +
			fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
	})
	p := &Player{client: c}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []service.PlayerEvent{service.PauseChanged{Paused: true}, service.FileLoaded{}}
	for _, w := range want {
		select {
		case got := <-p.Events():
			if got != w {
				t.Errorf("event = %#v, want %#v", got, w)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %#v", w)
		}
	}
}

func TestPlayerQuitStopsUnreadTranslation(t *testing.T) {
	c := newFakePair(t, func(req request) string {
		return `{"event":"property-change","name":"pause","data":true}` + "\n" + fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID)
	})
	exited := make(chan struct{})
	close(exited)
	p := &Player{client: c, exited: exited}
	out := p.Events()
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Quit(); err != nil {
		t.Fatal(err)
	}
	select {
	case ev, ok := <-out:
		if ok {
			t.Fatalf("Quit left a translated event: %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("Quit did not stop translator")
	}
}

func TestPlayerNaturalEOFDrainsTranslation(t *testing.T) {
	ours, theirs := net.Pipe()
	c := newClient(ours)
	defer c.Close()
	p := &Player{client: c}
	out := p.Events()
	go func() {
		defer theirs.Close()
		fmt.Fprintln(theirs, `{"event":"file-loaded"}`)
		fmt.Fprintln(theirs, `{"event":"playback-restart"}`)
	}()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("connection did not end")
	}
	for _, want := range []service.PlayerEvent{service.FileLoaded{}, service.PlaybackRestarted{}} {
		select {
		case got, ok := <-out:
			if !ok || got != want {
				t.Fatalf("event = %#v, open = %v, want %#v", got, ok, want)
			}
		case <-time.After(time.Second):
			t.Fatal("queued translation lost")
		}
	}
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(time.Second):
		t.Fatal("translator did not stop after EOF")
	}
}

func TestPlayerEventsConcurrentWithQuit(t *testing.T) {
	for range 20 {
		c := newFakePair(t, func(req request) string { return fmt.Sprintf(`{"request_id":%d,"error":"success"}`, req.RequestID) })
		exited := make(chan struct{})
		close(exited)
		p := &Player{client: c, exited: exited}
		var workers sync.WaitGroup
		for range 10 {
			workers.Go(func() { p.Events() })
			workers.Go(func() {
				if err := p.Quit(); err != nil {
					t.Errorf("Quit: %v", err)
				}
			})
		}
		workers.Wait()
		select {
		case _, ok := <-p.Events():
			if ok {
				t.Fatal("event after Quit")
			}
		case <-time.After(time.Second):
			t.Fatal("translator did not stop")
		}
	}
}
