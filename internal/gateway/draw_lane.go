package gateway

// The drawing lane's own cool-down and account rotation, apart from text's
// rests: an image refusal says nothing of the account's text (a ChatGPT
// account's images usage_limit_reached names when its drawing is back,
// resets_at and all), and text's rests say nothing of its drawing — one
// side's being out never benches the other. Image requests also have no
// session to stay sticky with, so the accounts that can draw take turns,
// spreading each one's load.

import (
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// drawRest is when an account's drawing lane is back, by the account's own
// restKey spelling (fallback.go) but in a table of its own that nothing of
// text's reads.
var drawRest struct {
	sync.Mutex
	until map[string]time.Time
}

// imageRefusalRest is how long an account's drawing sits out when its
// images were refused without a word of when the quota is back.
const imageRefusalRest = 10 * time.Minute

// drawLaneBack is when the account's drawing lane is back: the zero time
// when its images were never refused.
func drawLaneBack(key string) time.Time {
	drawRest.Lock()
	defer drawRest.Unlock()
	return drawRest.until[key]
}

// coolDrawLane benches an account's drawing alone until when. The account's
// text answers all the same.
func coolDrawLane(key string, when time.Time) {
	drawRest.Lock()
	defer drawRest.Unlock()
	if drawRest.until == nil {
		drawRest.until = map[string]time.Time{}
	}
	if t, ok := drawRest.until[key]; !ok || when.After(t) {
		drawRest.until[key] = when
	}
}

// drawLaneKeyOf is the drawing lane's key of an account-bearing provider:
// the same its candidates' restKey spells (fallback.go).
func drawLaneKeyOf(p provider.Provider) string {
	if a := p.Account; a != nil && a.User != "" {
		return p.ID + "@" + strings.ToLower(a.User)
	}
	return p.ID
}

// benchedDrawing sits a ChatGPT account's drawing out after its images were
// refused: until the reset the refusal named (usage_limit_reached's
// resets_at), or a plain while when it named none. Its text answers on.
func benchedDrawing(p provider.Provider, body []byte) {
	when := time.Now().Add(imageRefusalRest)
	if w := resetsAt(body, time.Now()); w > 0 {
		when = time.Now().Add(w)
	}
	coolDrawLane(drawLaneKeyOf(p), when)
}

// drawLaneOrder is the candidates as the drawing lane takes them: those
// whose lane is back first, and — behind them, for a request whose every
// other way failed — those whose lane is cooling, with when the earliest
// of those is back (the zero time when none is). Their order besides is
// the candidates' own (a provider set to Routing: Rotate takes its
// accounts in turns, which spreads each one's load).
func drawLaneOrder(cs []candidate) (ready, cooling []candidate, soonest time.Time) {
	now := time.Now()
	for _, c := range cs {
		if back := drawLaneBack(drawLaneKeyOf(c.p)); back.After(now) {
			cooling = append(cooling, c)
			if soonest.IsZero() || back.Before(soonest) {
				soonest = back
			}
			continue
		}
		ready = append(ready, c)
	}
	ready = forkDrawTurnOrder(ready) // fork(2026-10-28): 真轮转,见 fork_draw_turn.go
	return ready, cooling, soonest
}

// codexImageExtras passes the ChatGPT backend's own images params on as the
// caller sent them (its API takes moderation and output_compression on a
// generation, input_fidelity and partial_images on an edit too, as
// cli-proxy-api's direct image route forwards them).
func codexImageExtras(req map[string]any, d drawing, edit bool) {
	if v := strings.TrimSpace(d.Moderation); v != "" {
		req["moderation"] = v
	}
	if d.OutputCompression > 0 {
		req["output_compression"] = d.OutputCompression
	}
	if edit {
		if v := strings.TrimSpace(d.InputFidelity); v != "" {
			req["input_fidelity"] = v
		}
	}
	if d.PartialImages > 0 {
		req["partial_images"] = d.PartialImages
	}
}
