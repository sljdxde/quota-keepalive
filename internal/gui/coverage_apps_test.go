package gui

import (
	"testing"

	"qka/internal/status"
)

func TestCoverageHealthAndTodayAggregation(t *testing.T) {
	if healthRank("reauth_required") <= healthRank("disabled") ||
		healthRank("disabled") <= healthRank("healthy") {
		t.Fatal("health ranks should order healthy < disabled < reauth_required")
	}
	tests := []struct {
		states []string
		want   string
	}{
		{nil, "confirmed"},
		{[]string{"failed_retryable"}, "failed"},
		{[]string{"signed_provisional"}, "provisional"},
		{[]string{"signed_confirmed", "signed_provisional"}, "provisional"},
	}
	for _, tc := range tests {
		if got := worstToday(tc.states); got != tc.want {
			t.Errorf("worstToday(%v) = %q, want %q", tc.states, got, tc.want)
		}
	}
}

func TestAggregateAppsAddsUnknownAndDeduplicatesTargets(t *testing.T) {
	rows := AggregateApps(status.View{
		LastInvocation: "now",
		Accounts: []status.AccountStatus{
			{Target: "new-target", Enabled: false, SourceHealth: "healthy"},
		},
	}, []string{"new-target", "new-target", "unused-target"})
	rows = AggregateApps(status.View{
		LastInvocation: "now",
		Accounts: []status.AccountStatus{
			{Target: "new-target", Enabled: false, SourceHealth: "healthy"},
		},
	}, []string{"known", "known"})
	if len(rows) != 2 {
		t.Fatalf("unknown account target produced %d rows", len(rows))
	}
	if rows[1].Target != "new-target" || rows[1].AccountCount != 1 ||
		rows[1].AggregateHealth != "healthy" || rows[1].TodayResult != "confirmed" {
		t.Fatalf("unexpected aggregate row: %+v", rows[1])
	}

	seeded := AggregateApps(status.View{}, []string{"known", "known"})
	if len(seeded) != 1 || seeded[0].Target != "known" {
		t.Fatalf("duplicate seeded targets produced %+v", seeded)
	}
}
