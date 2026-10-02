package domain

import "testing"

func TestAudioDeviceLabel(t *testing.T) {
	tests := []struct {
		name string
		dev  AudioDevice
		want string
	}{
		{name: "description wins", dev: AudioDevice{Name: "coreaudio/0x44d9", Description: "External Headphones"}, want: "External Headphones"},
		{name: "falls back to name", dev: AudioDevice{Name: "auto"}, want: "auto"},
	}
	for _, tt := range tests {
		if got := tt.dev.Label(); got != tt.want {
			t.Errorf("%s: Label() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
