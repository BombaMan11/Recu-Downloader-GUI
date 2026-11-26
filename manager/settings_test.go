package manager

import (
	"strings"
	"testing"
)

// The Status sort order: the default, and an order from an older or edited
// settings file cleaned up (unknown and repeated names dropped, missing ones added).
func TestStatusOrder(t *testing.T) {
	want := []string{"downloading", "error", "finished", "checking", "waiting", "paused", "inactive"}
	if got := DefaultSettings().StatusOrder; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default %v", got)
	}
	s := Settings{StatusOrder: []string{"inactive", "nonsense", "waiting", "inactive"}}
	s.normalize()
	if got := strings.Join(s.StatusOrder, ","); got != "inactive,waiting,downloading,error,finished,checking,paused" {
		t.Errorf("cleaned %s", got)
	}
	s = Settings{}
	s.normalize()
	if strings.Join(s.StatusOrder, ",") != strings.Join(want, ",") {
		t.Errorf("empty became %v", s.StatusOrder)
	}
}

// Group Split Videos is off in a new install and goes out with the list.
func TestGroupSplitSetting(t *testing.T) {
	if DefaultSettings().GroupSplit {
		t.Error("on in a new install")
	}
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.GroupSplit = true })
	if !e.m.Snapshot().GroupSplit {
		t.Error("the list does not say the parts are grouped")
	}
}
