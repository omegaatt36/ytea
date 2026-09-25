package pipewire

import (
	"encoding/json"
	"errors"
	"testing"
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
