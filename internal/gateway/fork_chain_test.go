package gateway

// fork(2026-11-06): 身份链单测——mint 的 UUID 形态与唯一性、pck/thread_id
// 的提取与字节级替换、状态机的 told→ready→pinned 与 24h 淘汰、压缩 body
// 的 Anthropic 转换。gate 入口的判定分支由本地真号实测覆盖(钩子在
// testing.Testing() 下直通, 是 fork 的既定规约, 见 fork_draw_turn.go)。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestForkMintUUID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		k := forkMint()
		if len(k) != 36 || strings.Count(k, "-") != 4 {
			t.Fatalf("forkMint() = %q, want a uuid shape", k)
		}
		if seen[k] {
			t.Fatalf("forkMint() repeated %q", k)
		}
		seen[k] = true
	}
}

func TestForkPCKAndThreadID(t *testing.T) {
	body := []byte(`{"model":"gpt-6.1-sol","prompt_cache_key":"019a-b","client_metadata":{"thread_id":"019a-b","turn_id":"t1"},"input":[]}`)
	if got := forkPCK(body); got != "019a-b" {
		t.Fatalf("forkPCK = %q", got)
	}
	if got := forkThreadID(body); got != "019a-b" {
		t.Fatalf("forkThreadID = %q", got)
	}
	if got := forkPCK([]byte(`{"input":[]}`)); got != "" {
		t.Fatalf("forkPCK on absent = %q", got)
	}
}

func TestForkChainIdentitySwap(t *testing.T) {
	body := []byte(`{"instructions":"You are codex.","prompt_cache_key":"client-a","client_metadata":{"thread_id":"client-a","turn_id":"t9"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`)

	// the byte-level swap itself (forkChainIdentity's lookup is guarded
	// off under go test, as every fork hook is)
	got := forkSwapIdentity(body, "minted-b")
	if !strings.Contains(string(got), `"prompt_cache_key":"minted-b"`) {
		t.Fatalf("pck not swapped: %s", got)
	}
	if !strings.Contains(string(got), `"thread_id":"minted-b"`) {
		t.Fatalf("thread_id not swapped: %s", got)
	}
	if strings.Contains(string(got), "client-a") {
		t.Fatalf("old identity lingers: %s", got)
	}
	if !strings.Contains(string(got), `"turn_id":"t9"`) || !strings.Contains(string(got), "You are codex.") {
		t.Fatalf("unrelated fields disturbed: %s", got)
	}
	var round map[string]any
	if err := json.Unmarshal(got, &round); err != nil {
		t.Fatalf("swapped body no longer parses: %s", err)
	}
}

func TestForkChainStateMachine(t *testing.T) {
	chains.Lock()
	chains.m = map[string]*chainEntry{}
	chains.done = true
	chains.Unlock()

	if e := chainGet("nope"); e != nil {
		t.Fatalf("chainGet on empty = %v", e)
	}
	chainPut("conv", &chainEntry{Phase: chainTold})
	if e := chainGet("conv"); e == nil || e.Phase != chainTold {
		t.Fatalf("told not kept: %+v", e)
	}
	e := chainTouch("conv")
	if e == nil {
		t.Fatalf("touch lost the entry")
	}
	e.Phase, e.Key, e.Account = chainReady, "", ""
	chainPut("conv", e)
	if e := chainGet("conv"); e.Phase != chainReady {
		t.Fatalf("ready not kept: %+v", e)
	}
	// 24h idle retires the chain, as a stick retires
	e.LastSeen = time.Now().Add(-25 * time.Hour)
	chains.Lock()
	chainSave()
	chains.Unlock()
	if e := chainGet("conv"); e != nil {
		t.Fatalf("idle chain survived: %+v", e)
	}
	chainPut("conv2", &chainEntry{Phase: chainPinned, Key: "b", Account: "a@x"})
	chainDrop("conv2")
	if e := chainGet("conv2"); e != nil {
		t.Fatalf("dropped chain survived: %+v", e)
	}
}

func TestForkChainMessages(t *testing.T) {
	body := []byte(`{"instructions":"be brief","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug in main.go"}]},
		{"type":"reasoning","summary":[]},
		{"type":"function_call","name":"shell","arguments":"{\"cmd\":\"go test\"}"},
		{"type":"function_call_output","output":"all tests passed"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"main.go fixed"}]}
	]}`)
	msgs, err := forkChainMessages(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("messages = %d, want 5 (system note + 4 turns, reasoning dropped)", len(msgs))
	}
	if msgs[0]["role"] != "system" || !strings.Contains(msgs[0]["content"].(string), "be brief") {
		t.Fatalf("system note wrong: %v", msgs[0])
	}
	if msgs[1]["role"] != "user" || !strings.Contains(msgs[1]["content"].(string), "main.go") {
		t.Fatalf("first message wrong: %v", msgs[1])
	}
	if msgs[2]["role"] != "assistant" || !strings.Contains(msgs[2]["content"].(string), "shell") {
		t.Fatalf("tool call not bracketed: %v", msgs[2])
	}
	if msgs[3]["role"] != "user" || !strings.Contains(msgs[3]["content"].(string), "all tests passed") {
		t.Fatalf("tool output not carried: %v", msgs[3])
	}
	if msgs[4]["role"] != "assistant" || !strings.Contains(msgs[4]["content"].(string), "fixed") {
		t.Fatalf("reply dropped: %v", msgs[4])
	}
}

func TestForkContentTextShapes(t *testing.T) {
	if got := forkContentText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("string content = %q", got)
	}
	if got := forkContentText(json.RawMessage(`[{"type":"output_text","text":"a"},{"type":"output_text","text":"b"}]`)); !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Fatalf("array content = %q", got)
	}
	if got := forkContentText(nil); got != "" {
		t.Fatalf("empty content = %q", got)
	}
}

func TestForkChatTextAndGLMNames(t *testing.T) {
	if !strings.Contains(chainChatText, "新开") {
		t.Fatalf("chat text lost its instruction: %q", chainChatText)
	}
	if forkChainGLM == "" || forkGLMModel == "" {
		t.Fatalf("glm names unset")
	}
}

func TestForkChainCompactShapes(t *testing.T) {
	// 0.166-: a compaction_trigger input item on a plain /responses turn.
	trigger := []byte(`{"model":"gpt-6.1-sol","input":[{"type":"compaction_trigger"}],"stream":true}`)
	if !chainIsCompact(trigger) {
		t.Fatalf("trigger-style compact not caught")
	}
	// 0.167: no trigger, no instructions — the checkpoint prompt rides in
	// the last input message (read off the wire, 2026-11-09).
	checkpoint := []byte(`{"model":"gpt-6.1-sol","instructions":"","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task."}]}],"stream":true}`)
	if !chainIsCompact(checkpoint) {
		t.Fatalf("checkpoint-prompt compact not caught")
	}
	// an ordinary turn that merely talks about compaction stays a turn
	plain := []byte(`{"model":"gpt-6.1-sol","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"what does context checkpoint compaction mean?"}]}],"stream":true}`)
	if chainIsCompact(plain) {
		t.Fatalf("ordinary turn mistaken for a compact")
	}
}
