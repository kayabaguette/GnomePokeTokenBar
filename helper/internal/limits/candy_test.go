package limits

import (
	"reflect"
	"testing"
)

func TestCandyWindows(t *testing.T) {
	cases := []struct {
		name, body string
		want       []CandyWindow
	}{
		{"both, microseconds and offset",
			`{"five_hour":{"utilization":100,"resets_at":"2026-08-21T17:20:00.186770+00:00"},
			  "seven_day":{"utilization":30,"resets_at":"2026-08-25T23:00:00+02:00"}}`,
			[]CandyWindow{
				{"claude.fiveHour", CandySession, 100, "2026-08-21T17:20:00Z"},
				{"claude.sevenDay", CandyWeekly, 30, "2026-08-25T21:00:00Z"},
			}},
		{"Ultra: null five_hour",
			`{"five_hour":null,"seven_day":{"utilization":66,"resets_at":"2026-08-25T23:00:00.9Z"}}`,
			[]CandyWindow{{"claude.sevenDay", CandyWeekly, 66, "2026-08-25T23:00:00Z"}}},
		{"null utilization is skipped",
			`{"five_hour":{"utilization":null,"resets_at":"2026-08-21T17:20:00Z"},"seven_day":{"utilization":1,"resets_at":null}}`,
			[]CandyWindow{{"claude.sevenDay", CandyWeekly, 1, ""}}},
		{"unparseable reset falls back to the raw string",
			`{"five_hour":{"utilization":5,"resets_at":"soon"}}`,
			[]CandyWindow{{"claude.fiveHour", CandySession, 5, "soon"}}},
		{"opus, sonnet and scoped are excluded",
			`{"five_hour":null,"seven_day":null,"seven_day_opus":{"utilization":99,"resets_at":null},
			  "seven_day_sonnet":{"utilization":99,"resets_at":null},
			  "limits":[{"kind":"session","percent":100},{"kind":"weekly_scoped","percent":100}]}`,
			nil},
	}
	for _, c := range cases {
		got := CandyWindows(mustStatus(t, c.body), "claude")
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	// Additional-account prefix.
	got := CandyWindows(mustStatus(t, `{"five_hour":{"utilization":1,"resets_at":null}}`), "claude.1a2b3c4d")
	if len(got) != 1 || got[0].Key != "claude.1a2b3c4d.fiveHour" {
		t.Fatalf("%+v", got)
	}
}
