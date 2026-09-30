package pipewire

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const monitorFixture = `[
  {"id": 29, "type": "PipeWire:Interface:Node", "info": {"props": {"node.name": "Dummy-Driver"}}},
  {"id": 60, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "node.name": "hdmi"}}},
  {"id": 53, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Source", "node.name": "mic"}}},
  {"id": 317, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Stream/Output/Audio", "node.name": "ytea", "object.serial": 7974}}}
]
[
  {"id": 320, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Stream/Output/Audio", "node.name": "ytea", "object.serial": 8012}}}
]
[
  {"id": 317, "info": null}
]
[
  {"id": 88, "type": "PipeWire:Interface:Port", "info": {"props": {"port.name": "output_FL"}}}
]
[
  {"id": 320, "info": null}
]
`

func TestFollowStreams(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		node    string
		want    []int
		wantErr bool
	}{
		{name: "follows reopen and removal", input: monitorFixture, node: "ytea", want: []int{7974, 8012, 8012, 8012, 0}},
		{name: "sink is not a stream", input: monitorFixture, node: "hdmi", want: []int{0, 0, 0, 0, 0}},
		{name: "truncated update", input: monitorFixture + `[{"id": 1`, node: "ytea", want: []int{7974, 8012, 8012, 8012, 0}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []int
			err := followStreams(strings.NewReader(tt.input), tt.node, func(serial int) { got = append(got, serial) })
			if (err != nil) != tt.wantErr {
				t.Fatalf("followStreams() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("followStreams() reported %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumeStereoPCM(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tap := NewTap("ytea", 16)
		go tap.meter.Run(t.Context())
		pcm := make([]byte, 256*8)
		for i := range 256 {
			binary.LittleEndian.PutUint32(pcm[8*i:], math.Float32bits(0.5))
		}
		tap.consume(bytes.NewReader(pcm))
		time.Sleep(time.Second / 30)
		vu := <-tap.VU()
		if vu[0] < 0.2 || vu[1] != 0 {
			t.Fatalf("VU = %v, want left active, right silent", vu)
		}
	})
}
