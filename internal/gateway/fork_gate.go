package gateway

// fork(2026-11-06): codex 身份链的三个入口判定(状态机在 fork_chain.go,
// glm 压缩在 fork_compress.go)。
//
// forkChainGate 挂在 serve() 的 affine 之后(gateway.go, 选号循环之前):
// 仅对 codex provider 的文本模型生效, claude/kiro/gw-cc/生图 SKU 第一行
// 即直通。spent(粘住的号满)时不让路由静默换号——链上打回
// context_length_exceeded(SSE 错误码, codex-rs 读到即 stop-and-compact,
// 见 gateway.go streamError 的源码注释), chat 工具客户(无 compact 概念)
// 收 200 文案"请新开会话"; glm 代答压缩完成后(forkChainCompact 置
// ready), 客户端带薄历史回来时分娩新指纹钉到 cands[0](usage 自然序最空
// 的号)并放行正常计费; pinned 期间把钉号顶回第一位防路由漂移。
//
// forkChainCompact 挂在 codexBackend 的 /responses/compact 入口
// (compactOn 选号之前): 链上 told 阶段的压缩请求截给 glm, 摘要按
// codexCompact 的合成样板(四事件 SSE + compaction item + ForkEncrypted)
// 返回——客户端把摘要落为本地历史, 会话钥匙(session 头)不变。

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/codexsummary"
	"github.com/yetone/magpie/internal/provider"
)

// chainTellMost is how many tell-backs a told conversation gets before the
// gate gives up on it (a client that never compacts): past it the request
// goes on as upstream would have it, rather than looping the same error.
const chainTellMost = 5

// chainChatText is what a chat-tool client (no compaction of its own) is
// told when the conversation's account is spent.
const chainChatText = "【实验功能】上游账号额度已用尽，为避免降智本会话不再切换账号。请新开一个会话衔接当前任务。"

// forkChainGate returns true when it answered the request itself. cands and
// pl are taken as pointers because the pinned branch reorders them.
func forkChainGate(w http.ResponseWriter, r *http.Request, from provider.Protocol,
	aff *Affinity, cands *[]candidate, pl *planned, body []byte, model string, p *provider.Provider) bool {
	if forkChainOff || testing.Testing() {
		return false
	}
	if p == nil || p.ID != "codex" || strings.HasPrefix(model, "gpt-image-") {
		return false
	}
	if cands == nil || len(*cands) == 0 || !streamOf(body) {
		return false // a whole-body client keeps upstream's plain 429
	}
	key := chainOf(r.Header, body)
	e := chainTouch(key)

	// a conversation pinned to an account keeps that account first: the
	// minted key must never be sent anywhere else (a resting account the
	// router would move to, included)
	if e != nil && e.Phase == chainPinned {
		if aff != nil && aff.Why == "spent" {
			// the pinned account itself is spent: the chain moves on —
			// tell the client to compact, a fresh key is minted after
			e.Phase, e.Key, e.Tells = chainTold, "", 0
			chainPut(key, e)
		} else {
			at := chainAccountAt(*cands, e.Account)
			if at < 0 {
				chainDrop(key) // the account is gone; upstream routing takes over
				return false
			}
			if at > 0 {
				cs := *cands
				*cands = append(append([]candidate{cs[at]}, cs[:at]...), cs[at+1:]...)
				if pl != nil && at < len(pl.order) {
					pl.order = append(append([]Weighed{pl.order[at]}, pl.order[:at]...), pl.order[at+1:]...)
				}
			}
			return false
		}
	}

	if aff == nil || aff.Why != "spent" {
		return false
	}

	// ready: glm answered the compaction, the client came back with its
	// thin history — birth a fresh fingerprint onto the roomiest account
	// and let the request go on (billed as any other)
	if e != nil && e.Phase == chainReady {
		at := chainFirstAccount(*cands)
		if at < 0 {
			chainDrop(key)
			return false
		}
		e.Key = forkMint()
		e.Account = (*cands)[at].p.Account.User
		e.Phase = chainPinned
		chainPut(key, e)
		if at > 0 {
			cs := *cands
			*cands = append(append([]candidate{cs[at]}, cs[:at]...), cs[at+1:]...)
			if pl != nil && at < len(pl.order) {
				pl.order = append(append([]Weighed{pl.order[at]}, pl.order[:at]...), pl.order[at+1:]...)
			}
		}
		return false
	}

	// told (or first sight of a spent conversation): tell the client
	if e == nil {
		e = &chainEntry{Phase: chainTold}
		chainPut(key, e)
	} else if e.Phase != chainTold {
		e.Phase = chainTold
		e.Key = ""
	}
	e.Tells++
	chainPut(key, e)
	if e.Tells > chainTellMost {
		chainDrop(key) // this client never compacts: back to upstream behavior
		return false
	}
	chainTellBack(w, from, model, r.Header, body)
	return true
}

// forkChainCompact answers a /responses/compact of a told conversation from
// glm, moving the chain to ready. False lets magpie's own compaction
// routing take the request (the fallback when glm is down).
func forkChainCompact(w http.ResponseWriter, r *http.Request, body []byte) bool {
	if forkChainOff || testing.Testing() {
		return false
	}
	key := chainOf(r.Header, body)
	e := chainTouch(key)
	if e == nil || e.Phase != chainTold {
		return false
	}
	sum, err := forkGLMSummarize(r.Context(), body)
	if err != nil {
		log.Printf("fork chain: glm summary for %s failed: %s; compaction goes on as magpie routes it", firstWord(key), err)
		return false
	}
	e.Phase, e.Key, e.Tells = chainReady, "", 0
	chainPut(key, e)
	chainCompactReply(w, sum)
	return true
}

// chainTellBack is the spent tell: a codex client gets the error code it
// stops and compacts on (gateway.go streamError's comment carries the
// codex-rs citation); a chat-tool client gets a plain reply it can read.
func chainTellBack(w http.ResponseWriter, from provider.Protocol, model string, in http.Header, body []byte) {
	if chainIsAgent(in, body) {
		sw := newSSEWriter(w)
		sw.begun = true
		makeEncoder(from, sw, &Request{Model: model}).event(Event{
			Kind:   KError,
			Status: 413,
			Code:   "context_length_exceeded",
			Text:   "This conversation's context is full; compacting it to continue.",
		})
		return
	}
	sw := newSSEWriter(w)
	enc := makeEncoder(from, sw, &Request{Model: model})
	enc.event(Event{Kind: KText, Text: chainChatText})
	enc.event(Event{Kind: KStop})
}

// chainIsAgent: a session header or a prompt_cache_key means a client that
// compacts conversations of its own.
func chainIsAgent(in http.Header, body []byte) bool {
	for _, h := range sessionHeaders {
		if strings.TrimSpace(in.Get(h)) != "" {
			return true
		}
	}
	return forkPCK(body) != ""
}

// chainAccountAt is where the account's candidate sits, -1 when absent.
func chainAccountAt(cs []candidate, account string) int {
	if account == "" {
		return -1
	}
	for i := range cs {
		if cs[i].p.Account != nil && cs[i].p.Account.User == account {
			return i
		}
	}
	return -1
}

// chainFirstAccount is the first candidate that is one of the pool's
// accounts (a keyless entry would have nowhere to pin).
func chainFirstAccount(cs []candidate) int {
	for i := range cs {
		if cs[i].p.Account != nil && cs[i].p.Account.User != "" {
			return i
		}
	}
	return -1
}

// chainCompactReply writes the compaction answer as codexCompact does
// (codex_backend.go): a compaction item carrying the summary under the
// fork's own mark, in the four-event shape Codex takes.
func chainCompactReply(w http.ResponseWriter, sum string) {
	id := fmt.Sprintf("resp_fork_%d", time.Now().UnixNano())
	item := map[string]any{"type": "compaction", "id": "cmp_" + strings.TrimPrefix(id, "resp_"),
		"encrypted_content": codexsummary.ForkEncrypted(sum)}
	done := map[string]any{"id": id, "object": "response", "status": "completed", "output": []any{item}}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	for _, ev := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": item},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": done},
	} {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], b)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, '|'); i > 0 {
		return s[:i]
	}
	return s
}
