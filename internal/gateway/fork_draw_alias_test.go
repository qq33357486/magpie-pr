package gateway

import "testing"

func TestForkImageModelAlias(t *testing.T) {
	cases := []struct{ in, want string }{
		{"gpt-image-2.5", "gpt-image-2.6"},
		{"gpt-image-2.5-flare", "gpt-image-2.6-flare"},
		{"gpt-image-2.5-sunburst", "gpt-image-2.6-sunburst"},
		{"gpt-image-2.6", "gpt-image-2.6"},
		{"gpt-image-2", "gpt-image-2"},
		{"grok-imagine-image", "grok-imagine-image"},
		{"codex/gpt-image-2.5", "codex/gpt-image-2.5"}, // 带前缀的引用不改，别名只吃裸档位名
		{"", ""},
	}
	for _, c := range cases {
		if got := forkAliasImageModel(c.in); got != c.want {
			t.Fatalf("forkAliasImageModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
