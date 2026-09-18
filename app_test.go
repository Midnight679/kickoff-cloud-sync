package main

import "testing"

func TestFormatThousands(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{999999, "999,999"},
		{1234567, "1,234,567"},
	}
	for _, c := range cases {
		if got := formatThousands(c.n); got != c.want {
			t.Errorf("formatThousands(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
