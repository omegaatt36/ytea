package pipewire

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"
)

// Trimmed from real pw-dump output.
const dumpFixture = `[
  {"id": 29, "type": "PipeWire:Interface:Node", "info": {"props": {"node.name": "Dummy-Driver"}}},
  {"id": 60, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "node.name": "hdmi"}}},
  {"id": 53, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Source", "node.name": "mic"}}},
  {"id": 317, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Stream/Output/Audio", "node.name": "ytea", "object.serial": 7974}}}
]`

func TestFindStreamSerial(t *testing.T) {
	var objects []dumpObject
	if err := json.Unmarshal([]byte(dumpFixture), &objects); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	tests := []struct {
		name    string
		node    string
		want    int
		wantErr error
	}{
		{name: "stream found", node: "ytea", want: 7974},
		{name: "sink is not a stream", node: "hdmi", wantErr: errNodeNotFound},
		{name: "missing", node: "nope", wantErr: errNodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findStreamSerial(objects, tt.node)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("findStreamSerial(%q) error = %v, want %v", tt.node, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("findStreamSerial(%q) = %d, want %d", tt.node, got, tt.want)
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
