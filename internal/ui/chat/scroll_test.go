package chat

import "testing"

func TestAnchored(t *testing.T) {
	cases := []struct {
		name               string
		value, upper, page float64
		want               bool
	}{
		{"exact bottom", 900, 1000, 100, true},
		{"empty list", 0, 100, 100, true},
		{"sub-pixel slack within epsilon", 1000 - 100 - 23.9, 1000, 100, true},
		{"one pixel beyond epsilon", 1000 - 100 - 24.1, 1000, 100, false},
		{"scrolled to top", 0, 1000, 100, false},
		{"middle", 450, 1000, 100, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anchored(tc.value, tc.upper, tc.page); got != tc.want {
				t.Fatalf("anchored(%g,%g,%g) = %v, want %v",
					tc.value, tc.upper, tc.page, got, tc.want)
			}
		})
	}
}
