package gateway

// fork(2026-10-28): 生图真轮转。
//
// 渠道配的 routing=rotate 依赖 plugins/rotation 插件注册(routes.Find),
// 线上只装了 kiro-auth,rotate 一直静默退化成 Ordered——固定顺序,队首
// 账号扛到答不动为止(2026-10-28 usage.jsonl 实测: 下午 66 连发与 19 个
// 并发 504 全钉在队首 biped,次号 stack 闲置;同档位 sunburst 队首中位
// 93s vs 次号 49s)。生图车道在此自己轮转,不依赖插件,也不影响文本:
// 每个生图请求从 ready 账号的下一位开始试,白名单(fork_draw_whitelist)
// 与冷却分区之后生效,所以轮转只在真正可画的账号之间摊。
//
// testing.Testing() 时关闭:上游按 Ordered 写死的选号顺序断言
// (TestCodexDrawMovesToNextAccount 等)仍验证"拒绝移交下一个"的语义,
// 轮转本身由 fork_draw_turn_test.go 覆盖,不碰上游测试文件。

import (
	"sync"
	"testing"
)

// drawTurns is each provider's drawing pool turn counter: how many draws
// have picked a starting account there.
var drawTurns = struct {
	sync.Mutex
	m map[string]int
}{m: map[string]int{}}

// forkDrawTurnOrder rotates ready drawing candidates one further along per
// request, so a burst spreads over the accounts instead of piling onto the
// first listed one. A lane of fewer than two draws back as it came.
func forkDrawTurnOrder(ready []candidate) []candidate {
	if len(ready) < 2 || testing.Testing() {
		return ready
	}
	return rotateReady(ready, forkNextDrawTurn(ready[0].p.ID))
}

// forkNextDrawTurn is the pool's turn and counts one more.
func forkNextDrawTurn(pool string) int {
	drawTurns.Lock()
	n := drawTurns.m[pool]
	drawTurns.m[pool] = n + 1
	drawTurns.Unlock()
	return n
}

// rotateReady is ready begun n candidates along, wrapping.
func rotateReady(ready []candidate, n int) []candidate {
	out := make([]candidate, len(ready))
	for i := range ready {
		out[i] = ready[(n+i)%len(ready)]
	}
	return out
}
