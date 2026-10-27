package gateway

// fork(2026-10-27): 生图选号同样尊重账号白名单(provider.AccountModels)。
//
// 上游 drawOnAccounts 对 codex 恒走 listsCantName 分支: codex 账号的"自有模型清单"
// 取自 /codex/models 的聊天清单,本就列不出生图模型,上游因此把清单里没有生图模型的
// 账号一律放回生图候选(不然没有账号能画)。但运营侧用 provider/accountmodels 特意
// 划定的"只文本号"必须对生图同样生效——否则白名单对生图通道形同虚设(2026-10-27
// 线上实测: 生图请求漏到只文本号并出了图)。
//
// 判定与文本通道同源(gateway/fallback.go 的 AccountServes 调用): 没配清单的账号
// 不受影响; 档位名与实际 drawer 名任一在清单内即放行(白名单按对客 SKU 记,而
// drawOnAccounts 收到的 model 可能是解析后的 drawer)。

import (
	"errors"
	"slices"

	"github.com/yetone/magpie/internal/provider"
)

// forkDrawWhitelist filters drawing candidates by each account's own model
// list. Returns (nil, nil) when the provider has no lists (nothing to
// enforce), (kept, nil) after filtering, or (nil, err) when no listed
// account may draw — the caller refuses with a customer-facing Chinese 403.
func forkDrawWhitelist(p provider.Provider, cs []candidate, asked, drawer string) ([]candidate, error) {
	if p.Account == nil || len(p.AccountModels) == 0 || len(cs) == 0 {
		return nil, nil
	}
	kept := slices.DeleteFunc(cs, func(c candidate) bool {
		if c.p.Account == nil {
			return false
		}
		return !p.AccountServes(c.p.Account.User, asked) && !p.AccountServes(c.p.Account.User, drawer)
	})
	if len(kept) == 0 {
		return nil, errors.New("image generation is not open for this model yet; try again later")
	}
	return kept, nil
}
