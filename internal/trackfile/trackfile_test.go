package trackfile

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/omegaatt36/ytea/domain"
)

func TestTrackRoundTripAndKeys(t *testing.T) {
	want := domain.Track{ID: "abc", Title: "Song", Channel: "Artist", URL: "https://www.youtube.com/watch?v=abc", Duration: 212 * time.Second, Live: true}
	if got := From(want).To(); got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	data, err := json.Marshal(From(want))
	if err != nil {
		t.Fatal(err)
	}
	const doc = `{"ID":"abc","Title":"Song","Channel":"Artist","URL":"https://www.youtube.com/watch?v=abc","Duration":212000000000,"Live":true}`
	if string(data) != doc {
		t.Errorf("encoded = %s, want %s", data, doc)
	}
}

func TestAllKeepsNil(t *testing.T) {
	if FromAll(nil) != nil || ToAll(nil) != nil {
		t.Error("nil slices did not stay nil")
	}
	tracks := []domain.Track{{ID: "a", URL: "u"}}
	if got := ToAll(FromAll(tracks)); !reflect.DeepEqual(got, tracks) {
		t.Errorf("round trip = %+v, want %+v", got, tracks)
	}
}
