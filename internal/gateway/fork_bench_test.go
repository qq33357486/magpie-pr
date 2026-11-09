package gateway

import (
	"strings"
	"testing"
)

func TestForkSwapIdentityLargeBody(t *testing.T) {
	dialog := strings.Repeat(`{"type":"message","role":"user","content":[{"type":"input_text","text":"`+strings.Repeat("word ", 160)+`"}]},`, 1600)
	body := []byte(`{"instructions":"sys","prompt_cache_key":"bench-key-1","client_metadata":{"thread_id":"bench-key-1"},"input":[` + dialog[:len(dialog)-1] + `]}`)
	if len(body) < 1_400_000 {
		t.Fatalf("bench body too small: %d", len(body))
	}
	// warm
	forkSwapIdentity(body, "minted-bench")
	var worst int64
	for range 20 {
		t0 := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				forkSwapIdentity(body, "minted-bench")
			}
		})
		if ns := t0.T.Nanoseconds() / int64(t0.N); ns > worst {
			worst = ns
		}
	}
	t.Logf("body=%dKB swap worst=%dµs (avg per run)", len(body)/1024, worst/1000)
	if worst/1000 > 5000 { // 5ms
		t.Fatalf("identity swap too slow: %dµs", worst/1000)
	}
}
