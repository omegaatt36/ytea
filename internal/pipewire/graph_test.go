package pipewire

import (
	"errors"
	"reflect"
	"testing"
)

// Trimmed from real pw-dump output.
const dumpFixture = `[
  {"id": 29, "type": "PipeWire:Interface:Node", "info": {"props": {"node.name": "Dummy-Driver"}}},
  {"id": 40, "type": "PipeWire:Interface:Metadata", "props": {"metadata.name": "default"},
   "metadata": [
     {"subject": 0, "key": "default.configured.audio.sink", "type": "Spa:String:JSON", "value": {"name": "hdmi"}},
     {"subject": 0, "key": "default.audio.sink", "type": "Spa:String:JSON", "value": {"name": "usb_dac"}}
   ]},
  {"id": 60, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "node.name": "hdmi", "node.description": "HDMI Audio"}}},
  {"id": 45, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "node.name": "usb_dac", "node.description": "DX5 II Headphones"}}},
  {"id": 53, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Source", "node.name": "mic"}}},
  {"id": 317, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Stream/Output/Audio", "node.name": "ytea", "object.serial": 7974}}}
]`

func TestGraphSinks(t *testing.T) {
	g, err := ParseGraph([]byte(dumpFixture))
	if err != nil {
		t.Fatalf("ParseGraph() error = %v", err)
	}

	want := []Sink{
		{ID: 45, Name: "usb_dac", Description: "DX5 II Headphones", Default: true},
		{ID: 60, Name: "hdmi", Description: "HDMI Audio"},
	}
	if got := g.Sinks(); !reflect.DeepEqual(got, want) {
		t.Errorf("Sinks() = %+v, want %+v", got, want)
	}
}

func TestGraphStreamSerial(t *testing.T) {
	g, err := ParseGraph([]byte(dumpFixture))
	if err != nil {
		t.Fatalf("ParseGraph() error = %v", err)
	}

	tests := []struct {
		name    string
		node    string
		want    int
		wantErr error
	}{
		{name: "stream found", node: "ytea", want: 7974},
		{name: "sink is not a stream", node: "hdmi", wantErr: ErrNodeNotFound},
		{name: "missing", node: "nope", wantErr: ErrNodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.StreamSerial(tt.node)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("StreamSerial(%q) error = %v, want %v", tt.node, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("StreamSerial(%q) = %d, want %d", tt.node, got, tt.want)
			}
		})
	}
}

func TestParseGraphInvalid(t *testing.T) {
	if _, err := ParseGraph([]byte("{")); err == nil {
		t.Fatal("ParseGraph() error = nil, want error")
	}
}
