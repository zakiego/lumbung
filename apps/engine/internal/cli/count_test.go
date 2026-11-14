package cli

import "testing"

func TestCount(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "0 rows"}, {1, "1 row"}, {2, "2 rows"}, {1000, "1000 rows"}} {
		if got := count(c.n, "row"); got != c.want {
			t.Errorf("count(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
