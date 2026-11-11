package shortcuts

import (
	"reflect"
	"testing"
)

func TestAccelerators(t *testing.T) {
	cases := []struct {
		trigger string
		want    []string
	}{
		{"Press <Alt>Page_Up", []string{"<Alt>Page_Up"}},
		{"Drücke <Control><Shift>k oder F9", []string{"<Shift><Control>k", "F9"}},
		{"Press <Super>a or a", []string{"<Super>a"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := (Binding{Trigger: c.trigger}).Accelerators(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Accelerators(%q) = %q, want %q", c.trigger, got, c.want)
		}
	}
}
