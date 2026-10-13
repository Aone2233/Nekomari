package secretcmp

import "testing"

func TestEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "s3cret-value", "s3cret-value", true},
		{"empty and empty", "", "", true},
		{"different content", "s3cret-value", "s3cret-valuf", false},
		{"prefix only", "s3cret", "s3cret-value", false},
		{"empty and non-empty", "", "s3cret-value", false},
		{"different length", "abc", "abcd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Equal(c.a, c.b); got != c.want {
				t.Fatalf("Equal(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
			// 对称：参数顺序不该改变答案。
			if got := Equal(c.b, c.a); got != c.want {
				t.Fatalf("Equal(%q, %q) = %v, want %v (must be symmetric)", c.b, c.a, got, c.want)
			}
		})
	}
}
