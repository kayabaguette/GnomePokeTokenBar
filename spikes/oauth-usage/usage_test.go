package main

import (
	"encoding/json"
	"testing"
	"time"
)

const sample = `{
 "five_hour": {"utilization": 12.5, "resets_at": "2026-08-21T17:20:00.186770+00:00"},
 "seven_day": {"utilization": 40, "resets_at": null},
 "seven_day_opus": null,
 "seven_day_sonnet": {"utilization": 3, "resets_at": "2026-08-25T00:00:00+00:00"},
 "limits": [{"kind":"session","group":"session","percent":12.5,"severity":"ok",
   "resets_at":"2026-08-21T17:20:00.186770+00:00","is_active":true,
   "scope":{"model":{"display_name":"Sonnet"}}}]
}`

func TestDecode(t *testing.T) {
	var ls LimitStatus
	if err := json.Unmarshal([]byte(sample), &ls); err != nil {
		t.Fatal(err)
	}
	if ls.FiveHour == nil || ls.FiveHour.Utilization != 12.5 {
		t.Fatalf("five_hour: %+v", ls.FiveHour)
	}
	if _, err := time.Parse(time.RFC3339Nano, *ls.FiveHour.ResetsAt); err != nil {
		t.Fatal(err)
	}
	if ls.SevenDay.ResetsAt != nil || ls.SevenDayOpus != nil || ls.SevenDaySonnet == nil {
		t.Fatal("null handling")
	}
	if len(ls.Limits) != 1 || ls.Limits[0].Kind != "session" || !*ls.Limits[0].IsActive {
		t.Fatalf("limits: %+v", ls.Limits)
	}
	if len(describe(ls)) != 6 {
		t.Fatal("describe")
	}
}

func TestDecodeFiveHourNull(t *testing.T) {
	var ls LimitStatus
	if err := json.Unmarshal([]byte(`{"five_hour":null,"seven_day":{"utilization":1,"resets_at":null}}`), &ls); err != nil {
		t.Fatal(err)
	}
	if ls.FiveHour != nil || ls.Limits != nil {
		t.Fatal("expected nil")
	}
}

func TestExpiresAt(t *testing.T) {
	for _, c := range []string{`1800000000000`, `"1800000000000"`, `1800000000`, `"1800000000"`} {
		got, ok := parseExpiresAt(json.RawMessage(c))
		if !ok || got.Unix() != 1800000000 {
			t.Fatalf("%s -> %v %v", c, got, ok)
		}
	}
	if _, ok := parseExpiresAt(json.RawMessage(`"abc"`)); ok {
		t.Fatal("bad accepted")
	}
}
