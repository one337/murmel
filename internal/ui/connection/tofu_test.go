package connection

import "testing"

import "io.github/murmel/internal/certs"

func TestTOFUActionMapping(t *testing.T) {
	fp := "aa:bb"
	cases := []struct {
		name     string
		result   certs.VerifyResult
		declined string
		want     tofuAction
	}{
		{"match proceeds", certs.VerifyMatch, "", tofuProceed},
		{"match blocked after decline", certs.VerifyMatch, fp, tofuBlocked},
		{"new asks trust", certs.VerifyNew, "", tofuAskTrust},
		{"changed asks warning", certs.VerifyChanged, "", tofuAskChanged},
		{"changed asks warning despite decline", certs.VerifyChanged, fp, tofuAskChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tofuActionFor(tc.result, tc.declined); got != tc.want {
				t.Fatalf("tofuActionFor(%v, %q) = %v, want %v", tc.result, tc.declined, got, tc.want)
			}
		})
	}
}
