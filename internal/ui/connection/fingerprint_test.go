package connection

import "testing"

func TestFingerprintLines(t *testing.T) {
	fp := "00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:" +
		"f0:e1:d2:c3:b4:a5:96:87:78:69:5a:4b:3c:2d:1e:0f"
	want := "00:11:22:33:44:55:66:77\n88:99:aa:bb:cc:dd:ee:ff\n" +
		"f0:e1:d2:c3:b4:a5:96:87\n78:69:5a:4b:3c:2d:1e:0f"
	if got := fingerprintLines(fp); got != want {
		t.Fatalf("fingerprintLines =\n%s\nwant\n%s", got, want)
	}
	if got := fingerprintLines("aa:bb"); got != "aa:bb" {
		t.Fatalf("short fingerprint = %q, want unchanged", got)
	}
}
