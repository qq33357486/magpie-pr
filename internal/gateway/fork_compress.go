package gateway

// fork(2026-11-06): glm 代答压缩(身份链的一环, 入口在 fork_gate.go)。
//
// 客户端收到 context_length_exceeded 后发出的 /responses/compact 由这里
// 截住: 全量历史(含工具调用轮, 仅丢 tools 的 schema 定义——压缩用不上)
// 转成 chat messages 直发 glm-5.3-flash, 摘要按 fork_gate 的合成样板回给
// 客户端。glm 走 gw-cc provider 的 coding plan 端点(p.Chat 的
// /chat/completions, plan 配额), 绝不走 API/anthropic 计费端点(红线:
// 不烧智谱余额), 不经 new-api 不计费; 失败时 forkChainCompact 返回
// false, 压缩请求退回 magpie 自己的中继路由(不硬塞)。压缩指令要求结构
// 化摘要, 技术名词原样保留。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// forkChainGLM is the provider id whose key pays for the compaction
// summaries (an anthropic-compatible endpoint in providers.json).
const forkChainGLM = "gw-cc"

// forkGLMModel: 1M context, cheap enough to give away as part of the
// gate's service.
const forkGLMModel = "glm-5.3-flash"

// chainSummaryBudget is the summary's max_tokens (FORK_SUMMARY_BUDGET
// overrides, for when the quality pass wants more room).
func chainSummaryBudget() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("FORK_SUMMARY_BUDGET"))); err == nil && v > 0 {
		return v
	}
	return 4096
}

const chainSummaryInstruction = `你是会话压缩器。把下面这份 codex 编程助手与用户的完整对话历史，压缩成一份给"接手的助手"看的结构化摘要，让它能无缝继续任务。必须保留：
1. 任务目标与用户的核心要求（含显式偏好）
2. 已完成的工作与关键决定（为什么这么决定）
3. 涉及的文件路径、命令、API、代码标识符——原样保留，不得改写
4. 当前进行到哪一步、正在处理什么
5. 下一步计划与未解决的问题
用中文输出，技术名词保留英文。直接输出摘要正文，不要开场白。`

// forkGLMSummarize runs the compaction summary on glm and returns its text.
// The GLM provider here is a coding-plan endpoint (chat completions at
// p.Chat, its plan's own quota — never an API endpoint that would bill).
func forkGLMSummarize(ctx context.Context, body []byte) (string, error) {
	p, err := provider.Find(forkChainGLM)
	if err != nil {
		return "", fmt.Errorf("provider %s: %w", forkChainGLM, err)
	}
	keys := p.KeysOn()
	if len(keys) == 0 || strings.TrimSpace(keys[0].Key) == "" {
		return "", fmt.Errorf("provider %s has no key on", forkChainGLM)
	}
	base := p.Chat
	if base == "" {
		base = p.Base(provider.Chat)
	}
	if base == "" {
		return "", fmt.Errorf("provider %s has no chat base", forkChainGLM)
	}
	msgs, err := forkChainMessages(body)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	payload, err := json.Marshal(map[string]any{
		"model":      forkGLMModel,
		"max_tokens": chainSummaryBudget(),
		"messages": append([]map[string]any{{
			"role": "system", "content": chainSummaryInstruction,
		}}, msgs...),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	key := keys[0].Key
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("glm %s: %s", res.Status, truncateFork(string(b), 200))
	}
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return "", err
	}
	sum := ""
	if len(reply.Choices) > 0 {
		sum = strings.TrimSpace(reply.Choices[0].Message.Content)
	}
	if sum == "" {
		return "", fmt.Errorf("glm wrote no summary text")
	}
	return sum, nil
}

// forkChainMessages turns a codex Responses body into chat messages: every
// turn's text survives — tool calls and their outputs as bracketed text,
// the model's private reasoning left out. The instructions go back as a
// context note, not as a role to answer.
func forkChainMessages(body []byte) ([]map[string]any, error) {
	var req struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Name    string          `json:"name"`
			Args    string          `json:"arguments"`
			Output  string          `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("compaction body: %w", err)
	}
	msgs := []map[string]any{}
	if s := strings.TrimSpace(req.Instructions); s != "" {
		msgs = append(msgs, map[string]any{"role": "system",
			"content": "[原系统提示，仅供理解上下文]\n" + s})
	}
	for _, it := range req.Input {
		switch {
		case it.Type == "message" || it.Role == "user" || it.Role == "assistant":
			role := it.Role
			if role != "user" && role != "assistant" {
				continue
			}
			text := forkContentText(it.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			msgs = append(msgs, map[string]any{"role": role, "content": text})
		case it.Type == "function_call":
			msgs = append(msgs, map[string]any{"role": "assistant",
				"content": fmt.Sprintf("[调用了工具 %s] %s", it.Name, it.Args)})
		case it.Type == "function_call_output":
			msgs = append(msgs, map[string]any{"role": "user",
				"content": "[工具返回] " + truncateFork(it.Output, 6000)})
		}
	}
	// drop the leading system note if it ended up alone
	if len(msgs) == 1 && msgs[0]["role"] == "system" {
		return nil, fmt.Errorf("compaction body carried no turns")
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("compaction body carried no turns")
	}
	return msgs, nil
}

// forkContentText reads a Responses message's content: a plain string or
// an array of parts, their text joined.
func forkContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func truncateFork(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
