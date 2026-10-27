package main

import "testing"

func TestParseV4MappedPolicy(t *testing.T) {
	out := "Querying active state...\r\n\r\nPrecedence  Label  Prefix\r\n----------  -----  --------------------------------\r\n        50      0  ::1/128\r\n        40      1  ::/0\r\n        35      4  ::ffff:0:0/96\r\n        30      2  2002::/16\r\n"
	p, l, ok := parseV4MappedPolicy(out)
	if !ok || p != "35" || l != "4" {
		t.Fatalf("got %q %q %v", p, l, ok)
	}
	if _, _, ok := parseV4MappedPolicy("Precedence  Label  Prefix\n 40 1 ::/0\n"); ok {
		t.Fatal("expected not found")
	}
}
