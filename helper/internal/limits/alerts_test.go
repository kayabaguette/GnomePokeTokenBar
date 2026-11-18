package limits

import (
	"reflect"
	"testing"
)

func wi(key string, u float64) WindowInfo {
	return WindowInfo{Key: key, Kind: KindSevenDay, Utilization: u}
}

func TestAlertsEdgeTriggered(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UsageStoreTests.swift:509 (80 warn; 81-94 silent; 95 crit; 96, 99 silent)
	tiers := map[string]int{}
	steps := []struct {
		u    float64
		want string // "" = no alert
	}{
		{80, "warn"}, {81, ""}, {84, ""}, {90, ""}, {94, ""}, {95, "crit"}, {96, ""}, {99, ""},
	}
	for _, s := range steps {
		var alerts []Alert
		tiers, alerts = EvaluateAlerts(tiers, []WindowInfo{wi("k", s.u)}, 80, 95)
		got := ""
		if len(alerts) == 1 {
			got = alerts[0].Tier
		} else if len(alerts) > 1 {
			t.Fatalf("u=%v: %d alerts", s.u, len(alerts))
		}
		if got != s.want {
			t.Errorf("u=%v: got %q want %q", s.u, got, s.want)
		}
	}
}

func TestAlertsRearmAndSkipToCrit(t *testing.T) {
	// Swift: UsageStoreTests.swift:534 (82, 40, then 85 -> warn again)
	tiers, a := EvaluateAlerts(nil, []WindowInfo{wi("k", 82)}, 80, 95)
	if len(a) != 1 || a[0].Tier != "warn" {
		t.Fatalf("%v", a)
	}
	tiers, a = EvaluateAlerts(tiers, []WindowInfo{wi("k", 40)}, 80, 95)
	if len(a) != 0 || len(tiers) != 0 {
		t.Fatalf("drop below warn must clear the key: %v %v", tiers, a)
	}
	_, a = EvaluateAlerts(tiers, []WindowInfo{wi("k", 85)}, 80, 95)
	if len(a) != 1 || a[0].Tier != "warn" {
		t.Fatalf("%v", a)
	}
	// Straight to crit from nothing is one crit alert; a crit that falls back to warn is silent.
	tiers, a = EvaluateAlerts(nil, []WindowInfo{wi("k", 99)}, 80, 95)
	if len(a) != 1 || a[0].Tier != "crit" {
		t.Fatalf("%v", a)
	}
	tiers, a = EvaluateAlerts(tiers, []WindowInfo{wi("k", 85)}, 80, 95)
	if len(a) != 0 || tiers["k"] != 1 {
		t.Fatalf("crit -> warn: %v %v", tiers, a)
	}
	_, a = EvaluateAlerts(tiers, []WindowInfo{wi("k", 96)}, 80, 95)
	if len(a) != 1 || a[0].Tier != "crit" {
		t.Fatalf("warn -> crit again: %v", a)
	}
}

func TestAlertsPerKeyIndependenceAndPruning(t *testing.T) {
	// Swift: UsageStoreTests.swift:545 (windows tracked independently)
	label := "Opus"
	prev := map[string]int{"claude.fiveHour": 2, "gone": 2}
	ws := []WindowInfo{
		wi("claude.fiveHour", 99),
		{Key: "claude.scoped.x.Opus.2", Kind: KindScoped, Label: &label, Utilization: 85},
		wi("claude.sevenDay", 10),
	}
	next, alerts := EvaluateAlerts(prev, ws, 80, 95)
	want := []Alert{{WindowKey: "claude.scoped.x.Opus.2", WindowKind: KindScoped, Label: &label, Tier: "warn", Utilization: 85}}
	if !reflect.DeepEqual(alerts, want) {
		t.Fatalf("alerts: %+v", alerts)
	}
	if !reflect.DeepEqual(next, map[string]int{"claude.fiveHour": 2, "claude.scoped.x.Opus.2": 1}) {
		t.Fatalf("next (gone must be pruned): %v", next)
	}
	if prev["gone"] != 2 || len(prev) != 2 {
		t.Fatal("prev must not be modified")
	}
}

func TestAlertsDuplicateKeyAndThresholdEdge(t *testing.T) {
	next, a := EvaluateAlerts(nil, []WindowInfo{wi("k", 95), wi("k", 80)}, 80, 95)
	if len(a) != 1 || next["k"] != 2 {
		t.Fatalf("%v %v", next, a)
	}
	// Exactly at the thresholds counts (>=).
	_, a = EvaluateAlerts(nil, []WindowInfo{wi("a", 80), wi("b", 79.99)}, 80, 95)
	if len(a) != 1 || a[0].WindowKey != "a" {
		t.Fatalf("%v", a)
	}
}
