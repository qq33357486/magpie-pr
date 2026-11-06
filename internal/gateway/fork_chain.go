package gateway

// fork(2026-11-06): codex 身份链——防"会话跨账号"暴露。
//
// 背景: 号满切号时, 客户端的完整上下文带着同一个 prompt_cache_key 在新
// 账号上重放, 是内容级"多账号服务同一用户"铁证(社区实证可连坐)。本文件
// 维护客户端会话→上游指纹的链: 客户端一生只有会话 a; 号满时 fork_gate
// 打回 context_length_exceeded 让客户端自行压缩(历史在客户端变薄),
// 压缩的 summarize 由 fork_compress 转 glm 代答; 压缩完成后本链"分娩"
// 新指纹 b(随机 UUID, 与 a 零派生关系)钉到有额度的新号, 此后 a 的所有
// 请求出站时 pck+thread_id 都替换为 b; 再满则 b→c。上游每个账号只见过
// 一个指纹, 且接到的都是薄摘要历史。
//
// 链键 = conversationID(入站 session 头优先, codex CLI 每请求带且 compact
// 不换 thread → 跨压缩稳定; 无头 chat 客户退化为开头 hash)。状态空闲
// 24h 淘汰(与 affinity.json 的 stickKeep 同口径), 持久化 fork_chain.json
// 防重启丢链。identity 改写是字节级精准替换(不重排 JSON 字段, 1.5MB
// 会话两次线性扫描, 远轻于全量 unmarshal)。
//
// testing.Testing() 时三个钩子入口(forkChainGate/forkChainCompact/
// forkChainIdentity)全部直通, 上游测试不受影响; 本文件自身的状态机/替换
// 逻辑由 fork_chain_test.go 覆盖。

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// chain phases: told = 已打回"上下文满了", 等客户端压缩; ready = glm 已
// 代答压缩, 等客户端带薄历史回来分娩; pinned = 指纹已分娩并钉在账号上。
const (
	chainTold   = "told"
	chainReady  = "ready"
	chainPinned = "pinned"
)

// chainKeep is how long an idle chain entry stays: as long as magpie keeps
// a stick (24h), matching the conversation's own lifetime here.
const chainKeep = 24 * time.Hour

// chainEntry is one client conversation's place in the identity chain.
type chainEntry struct {
	Key      string    // the minted upstream fingerprint ("" until birth)
	Account  string    // the account the fingerprint is pinned to
	Phase    string    // told / ready / pinned
	Tells    int       // tell-backs handed out in this told stretch
	LastSeen time.Time // refreshed on every request of the conversation
}

var chains = struct {
	sync.Mutex
	m    map[string]*chainEntry
	done bool // loaded?
}{m: map[string]*chainEntry{}}

// forkChainOff is the one-switch kill: FORK_CHAIN=off returns the gateway
// to upstream behavior with no reload.
var forkChainOff = func() bool {
	return envForkChainOff()
}()

func envForkChainOff() bool {
	return os.Getenv("FORK_CHAIN") == "off"
}

// chainPath is fork_chain.json beside magpie's other state (affinity.json).
func chainPath() string {
	return filepath.Join(filepath.Dir(provider.Path()), "fork_chain.json")
}

// chainLoad reads the persisted chain once; a missing or unreadable file
// starts an empty chain (identities are minted again as needed — an
// account never sees a key it shouldn't, only a fresh one).
func chainLoad() {
	chains.Lock()
	defer chains.Unlock()
	if chains.done {
		return
	}
	chains.done = true
	var persisted map[string]*chainEntry
	if b, err := os.ReadFile(chainPath()); err == nil {
		_ = json.Unmarshal(b, &persisted)
	}
	now := time.Now()
	for k, e := range persisted {
		if e == nil || now.Sub(e.LastSeen) > chainKeep {
			continue
		}
		chains.m[k] = e
	}
}

// chainSave persists the chain under the lock's caller (already held).
// Skipped under go test: the state machine itself is covered in memory,
// and the round-trip belongs to the live gate test.
func chainSave() {
	if testing.Testing() {
		return
	}
	out := map[string]*chainEntry{}
	for k, e := range chains.m {
		out[k] = e
	}
	if b, err := json.MarshalIndent(out, "", " "); err == nil {
		_ = os.WriteFile(chainPath(), b, 0o600)
	}
}

// chainOf is this request's conversation as the chain knows it.
func chainOf(in http.Header, body []byte) string {
	return conversationID(in, body)
}

// chainGet returns the conversation's entry, or nil. Idle entries expire
// on read, as a stick does.
func chainGet(key string) *chainEntry {
	chainLoad()
	chains.Lock()
	defer chains.Unlock()
	e := chains.m[key]
	if e == nil {
		return nil
	}
	if time.Since(e.LastSeen) > chainKeep {
		delete(chains.m, key)
		chainSave()
		return nil
	}
	return e
}

// chainTouch marks the conversation seen and returns its entry (may be nil).
func chainTouch(key string) *chainEntry {
	chainLoad()
	chains.Lock()
	defer chains.Unlock()
	e := chains.m[key]
	if e == nil {
		return nil
	}
	if time.Since(e.LastSeen) > chainKeep {
		delete(chains.m, key)
		chainSave()
		return nil
	}
	e.LastSeen = time.Now()
	return e
}

// chainPut stores a transition and persists it.
func chainPut(key string, e *chainEntry) {
	chains.Lock()
	defer chains.Unlock()
	e.LastSeen = time.Now()
	chains.m[key] = e
	chainSave()
}

// chainDrop forgets a conversation (its pinned account is gone).
func chainDrop(key string) {
	chains.Lock()
	defer chains.Unlock()
	delete(chains.m, key)
	chainSave()
}

// forkMint births a fresh fingerprint: a random UUIDv4 with zero derived
// relation to the client's own key or to any earlier one in the chain —
// two identities the upstream sees can never be linked by their values.
func forkMint() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not survivable in any mode; fall back to
		// time-seeded bytes rather than mint twice the same key
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (uint(i%8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// forkPCK reads the body's top-level prompt_cache_key, "" when the client
// sent none (chat-tool clients).
func forkPCK(body []byte) string {
	return forkJSONString(body, `"prompt_cache_key":"`)
}

// forkThreadID reads client_metadata's thread_id, "" when absent. A real
// codex client keeps it equal to the prompt_cache_key; both are replaced
// together so the pair stays a shape the real client sends (#5727's
// lesson: rewriting one alone makes an impossible combination).
func forkThreadID(body []byte) string {
	i := bytes.Index(body, []byte(`"client_metadata"`))
	if i < 0 {
		return ""
	}
	return forkJSONString(body[i:], `"thread_id":"`)
}

// forkJSONString finds pat's `"name":"value"` and returns value.
func forkJSONString(body []byte, pat string) string {
	i := bytes.Index(body, []byte(pat))
	if i < 0 {
		return ""
	}
	rest := body[i+len(pat):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 || j > 128 { // a uuid never runs long
		return ""
	}
	return string(rest[:j])
}

// forkChainIdentity rewrites the outbound body's conversation identity to
// the chain's minted fingerprint: the same key on the pinned account every
// time (its cache stays), a key no other account has ever seen. Only the
// pinned account's requests are rewritten; anywhere else the body goes as
// the client wrote it.
func forkChainIdentity(in http.Header, body []byte, account string) []byte {
	if forkChainOff || testing.Testing() || account == "" {
		return body
	}
	key := chainOf(in, body)
	e := chainTouch(key)
	if e == nil || e.Phase != chainPinned || e.Key == "" || e.Account != account {
		return body
	}
	out := forkSwapIdentity(body, e.Key)
	if string(out) != string(body) {
		log.Printf("fork chain identity: conv=%s on %s pck %s -> %s",
			firstWord(key), firstWord(account), forkPCK(body), e.Key)
	}
	return out
}

// forkSwapIdentity is the byte-level swap itself, kept apart for its test.
func forkSwapIdentity(body []byte, minted string) []byte {
	pck := forkPCK(body)
	if pck == "" || pck == minted {
		return body
	}
	out := bytes.ReplaceAll(body,
		[]byte(`"prompt_cache_key":"`+pck+`"`),
		[]byte(`"prompt_cache_key":"`+minted+`"`))
	if thid := forkThreadID(out); thid != "" && thid != minted {
		out = bytes.ReplaceAll(out,
			[]byte(`"thread_id":"`+thid+`"`),
			[]byte(`"thread_id":"`+minted+`"`))
	}
	return out
}
