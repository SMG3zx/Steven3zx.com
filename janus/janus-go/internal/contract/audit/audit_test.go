package audit

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEventSurvivesEncoding(t *testing.T) {
	sent := Event{Level: "warn", Name: "build.failed", Fields: map[string]string{"build": "b1"}}
	data, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"level":"warn","name":"build.failed","fields":{"build":"b1"}}` {
		t.Fatalf("encoded as %s", data)
	}
	var received Event
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, sent) {
		t.Fatalf("decoded %+v, want %+v", received, sent)
	}
}

func TestSubjectIsOneTokenPerService(t *testing.T) {
	for service, want := range map[string]string{
		"builds":   "janus.audit.builds",
		"a.b":      "janus.audit.a_b",
		"x*>":      "janus.audit.x__",
		"":         "janus.audit.unknown",
		"two word": "janus.audit.two_word",
	} {
		if got := Subject(service); got != want {
			t.Errorf("Subject(%q) = %q, want %q", service, got, want)
		}
	}
}
