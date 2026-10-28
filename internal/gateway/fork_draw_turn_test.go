package gateway

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// fork(2026-10-28): 生图真轮转的行为。轮转在测试下哨兵关闭
// (forkDrawTurnOrder 原样返回),这里直测纯函数与计数器。

func drawTurnCand(pool, user string) candidate {
	return candidate{p: provider.Provider{ID: pool, Account: &provider.Account{User: user}}, rest: user}
}

func stringify(cs []candidate) string {
	out := ""
	for _, c := range cs {
		out += c.rest
	}
	return out
}

func TestRotateReadyBeginsNAlong(t *testing.T) {
	ready := []candidate{
		drawTurnCand("codex-turn", "a"),
		drawTurnCand("codex-turn", "b"),
		drawTurnCand("codex-turn", "c"),
	}
	for n, want := range []string{"abc", "bca", "cab", "abc"} {
		if have := stringify(rotateReady(ready, n)); have != want {
			t.Fatalf("n=%d: order %q, want %q", n, have, want)
		}
	}
	if have := stringify(rotateReady(ready[:1], 7)); have != "a" {
		t.Fatalf("a one-account lane is itself whatever the turn, got %q", have)
	}
}

func TestForkNextDrawTurnCountsPerPool(t *testing.T) {
	const p1, p2 = "codex-count-1", "codex-count-2"
	if n := forkNextDrawTurn(p1); n != 0 {
		t.Fatalf("a fresh pool starts its count at 0, got %d", n)
	}
	forkNextDrawTurn(p1)
	if n := forkNextDrawTurn(p1); n != 2 {
		t.Fatalf("the pool counts its own draws, got %d", n)
	}
	if n := forkNextDrawTurn(p2); n != 0 {
		t.Fatalf("another pool counts apart, got %d", n)
	}
}

func TestForkDrawTurnOrderIsAsleepUnderTests(t *testing.T) {
	ready := []candidate{
		drawTurnCand("codex-asleep", "a"),
		drawTurnCand("codex-asleep", "b"),
	}
	forkNextDrawTurn("codex-asleep") // 预热计数,证明睡的不是"恰好第 0 轮"
	if have := stringify(forkDrawTurnOrder(ready)); have != "ab" {
		t.Fatalf("under tests the hook keeps upstream's order, got %q", have)
	}
}

func TestDrawLaneOrderKeepsCoolingBehind(t *testing.T) {
	const pool = "codex-lane"
	a := drawTurnCand(pool, "a")
	b := drawTurnCand(pool, "b")
	c := drawTurnCand(pool, "c")
	back := time.Now().Add(10 * time.Minute)
	const bench = pool + "@c" // drawLaneKeyOf:有账号时为 pool@user(小写)
	coolDrawLane(bench, back)
	defer func() {
		drawRest.Lock()
		delete(drawRest.until, bench)
		drawRest.Unlock()
	}()

	ready, cooling, soonest := drawLaneOrder([]candidate{a, b, c})
	if len(cooling) != 1 || cooling[0].rest != "c" {
		t.Fatalf("the cooled account waits behind, got cooling=%q", stringify(cooling))
	}
	if soonest.Before(back.Add(-time.Second)) {
		t.Fatalf("soonest tells when the cooled lane is back: %v", soonest)
	}
	if have := stringify(ready); have != "ab" {
		t.Fatalf("the drawable accounts come in listed order under tests, got %q", have)
	}
}
