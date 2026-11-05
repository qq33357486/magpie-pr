package gateway

// fork(2026-11-05): 生图档位路由 2.5→2.6。new-api 渠道的 model_mapping 在透传模式
// (pass_through_body_enabled，"中间层不改写请求体"定规) 下不生效——透传按原始字节
// 转发、不重建请求体，映射只能在 magpie 入口做。别名改写后选号/白名单/usage 均按
// 实际模型走；客户侧账面(new-api logs)仍记其请求的 2.5 名。撤路由=清空此表。

var forkImageModelAlias = map[string]string{
	"gpt-image-2.5":          "gpt-image-2.6",
	"gpt-image-2.5-flare":    "gpt-image-2.6-flare",
	"gpt-image-2.5-sunburst": "gpt-image-2.6-sunburst",
}

func forkAliasImageModel(model string) string {
	if v, ok := forkImageModelAlias[model]; ok {
		return v
	}
	return model
}
