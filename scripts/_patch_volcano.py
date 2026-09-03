import io

# 1) provider: 火山 anthropic 网关的模型清单修正
#    /v1/models 在火山返回全量 130 个 ark 模型（coding plan 无独立清单 API），
#    AnthropicProvider 检测到火山域名时改为返回 coding plan 支持的精选视觉模型
p = 'internal/provider/provider.go'
s = io.open(p, encoding='utf-8').read()

old = '''// ListModels 拉取模型清单（Anthropic /v1/models）
func (a *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {'''
new = '''// volcanoCodingPlanModels 火山 coding plan 实测支持的视觉模型精选
// （网关 /v1/models 返回全量 ark 目录 130+，其中大量模型不在 coding plan
//   内、调用会报 UnsupportedModel；该清单来自实测排查）
var volcanoCodingPlanModels = []string{
	"doubao-seed-2-0-pro-260215",
	"doubao-seed-2-0-lite-260428",
	"doubao-seed-2-0-mini-260428",
	"doubao-seed-2-0-pro-260628",
	"doubao-seed-2-1-pro-260628",
	"doubao-seed-2-1-turbo-260628",
	"doubao-seed-1-6-251015",
	"doubao-seed-1-6-flash-250828",
	"doubao-seed-1-6-lite-251015",
	"doubao-seed-1-6-vision-250815",
	"doubao-seed-1-6-thinking-250715",
	"doubao-1-5-thinking-vision-pro-250428",
	"doubao-1-5-vision-pro-250328",
	"doubao-1.5-vision-lite-250315",
	"doubao-vision-pro-32k-241028",
	"doubao-vision-lite-32k-241015",
}

// ListModels 拉取模型清单（Anthropic /v1/models）
func (a *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {'''
assert old in s, 'm1'
s = s.replace(old, new)

old = '''	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}'''
new = '''	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	// 火山 coding plan 网关的 /v1/models 返回全量 ark 目录（含大量不在
	// coding plan 内的模型，调用会报 UnsupportedModel）；按实测清单过滤
	if strings.Contains(a.cfg.Provider.BaseURL, "ark.cn-beijing.volces.com") ||
		strings.Contains(a.cfg.Provider.BaseURL, "/api/coding") {
		inPlan := map[string]bool{}
		for _, m := range volcanoCodingPlanModels {
			inPlan[m] = true
		}
		filtered := make([]string, 0, len(ids))
		for _, id := range ids {
			if inPlan[id] {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) > 0 {
			return filtered, nil
		}
		// 实测清单与平台目录不匹配（模型下线等）时退回全量并提示
	}
	return ids, nil
}'''
assert old in s, 'm2'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('volcano models filter ok')
print('ALL OK')
