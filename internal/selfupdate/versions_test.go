package selfupdate

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{a: "1.2.3", b: "1.2.3", want: 0},
		{a: "v1.2.4", b: "1.2.3", want: 1},
		{a: "V1.2.3", b: "v1.2.4", want: -1},
		{a: "2.0", b: "1.9.9", want: 1},
		{a: "1", b: "1.0.1", want: -1},
		{a: "1.0.dev", b: "1.0.1", want: -1},
	}
	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
