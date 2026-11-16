package coverage

import (
	"testing"
	"time"
)

func TestPythonClassificationContract(t *testing.T) {
	cases := []struct {
		e     Evidence
		fresh bool
		want  string
	}{
		{Evidence{Identity: 1, Users: 1, Recent: 2}, true, "covered"},
		{Evidence{Identity: 1, Users: 1, Recent: 1, Old: 2}, true, "partial"},
		{Evidence{Identity: 1, Users: 1}, true, "gap"},
		{Evidence{Identity: 1}, true, "unknown"},
		{Evidence{Identity: 1, Users: 2, Recent: 1}, true, "unknown"},
		{Evidence{Identity: 1, Users: 1, Recent: 1, Ambiguous: 1}, true, "unknown"},
		{Evidence{Identity: 2, Users: 1, Recent: 1}, true, "unknown"},
		{Evidence{Identity: 1, Users: 1, Recent: 1}, false, "unknown"},
	}
	for _, c := range cases {
		if got := Classify(c.e, c.fresh); got != c.want {
			t.Errorf("%+v fresh=%v got %s want %s", c.e, c.fresh, got, c.want)
		}
	}
}
func TestFreshnessBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		status string
		at     time.Time
		want   bool
	}{{"completed", now.Add(-24 * time.Hour), true}, {"completed", now.Add(-24*time.Hour - time.Second), false}, {"completed", now.Add(time.Second), false}, {"partial", now, false}, {"failed", now, false}, {"completed", time.Time{}, false}} {
		if got := Fresh(c.status, c.at, now); got != c.want {
			t.Errorf("%+v got %v", c, got)
		}
	}
}
