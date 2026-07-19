package main

import "testing"

// The "requested :6969 was in use" boot line must only fire when the port
// actually moved. A bare ":6969" bind comes back from the kernel as
// "[::]:6969", and the old raw string comparison printed the fallback
// warning on every clean boot.
func TestSameListenPort(t *testing.T) {
	cases := []struct {
		requested, actual string
		want              bool
	}{
		{":6969", "[::]:6969", true},
		{":6969", "0.0.0.0:6969", true},
		{"127.0.0.1:6969", "127.0.0.1:6969", true},
		{":6969", "[::]:6970", false},
		{":6969", "0.0.0.0:6975", false},
		{"not-an-addr", "not-an-addr", true},
	}
	for _, c := range cases {
		if got := sameListenPort(c.requested, c.actual); got != c.want {
			t.Errorf("sameListenPort(%q, %q) = %v, want %v", c.requested, c.actual, got, c.want)
		}
	}
}
