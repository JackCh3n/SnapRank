// Package scorer 包含评分 Prompt 构造、模型输出解析校验与本地加权总分计算。
// 设计约束（v1.2）：模型只输出维度分，总分由本地按当前权重加权，
// 权重调整后可基于已存维度分零成本重算。
package scorer

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"snaprank/internal/config"
	"snaprank/internal/store"
)

// PromptVersion Prompt 文本版本，随内容变更递增；缓存按版本隔离
// v3: 对齐国际摄影比赛（PSA/FIAP）评审标准，显著收紧给分尺度
const PromptVersion = "v3"

// dimOrder 维度键顺序
var dimKeys = []string{"technique", "composition", "content", "color"}

// BuildPrompt 构造评分 Prompt
// 评分尺度对齐国际摄影沙龙/比赛的通行评审：accepted 线通常在 7-8/10，
// 9 分以上仅授予有获奖实力的作品。日常随手拍多数应落在 3-6 分。
func BuildPrompt() string {
	return `你是一位国际摄影比赛（PSA/FIAP 级别）的资深评审。请以比赛评审的严格标准为这张照片打分。

【评分维度】（每项 0-10 分，可用 0.5 间隔，全档可用，不要集中在中间段）
1. technique 技术质量：锐度与焦点准确性、噪点控制、曝光精确性、动态范围运用、景深控制
2. composition 构图：主体是否明确突出、视觉引导、画框利用、元素平衡、背景干扰、独特视角
3. content 内容与情感：主题表达、叙事性、决定性瞬间、情绪感染力、是否"有话要说"
4. color 色彩：白平衡准确、色调和谐、影调层次、色彩为表达服务而非堆砌

【评分锚点】（国际比赛尺度，严格执行）
9-10：获奖级。技术无可挑剔 + 构图有独创性 + 有强烈表达或决定性瞬间。此类作品可获奖章，极罕见
8-8.9：入选级。接近获奖水平但有可指出的瑕疵；比赛 accepted 水平
7-7.9：良好。技术扎实、构图完整，但缺乏令人记住的亮点；业余优秀水平
5-6.9：一般。存在明显技术缺陷（跑焦/噪点/过曝）或构图平淡（居中随手拍、背景杂乱）
3-4.9：差。多处硬伤：严重偏焦、大量噪点、构图混乱、主体不明
0-2.9：废片。严重干扰观看，无保留价值

【评审纪律】
- 默认从严：一张照片必须"赢得"分数，而不是"证明自己没有问题"
- 主体不突出、视角平常、内容平淡的照片，content 和 composition 不应超过 6
- 8 分以上的判断必须能写出具体理由
- 禁止把所有照片都打进 6-8 的"舒适区"，好照片和差照片必须拉开差距

【输出要求】只输出一个 JSON 对象，不要任何解释文字：
{"dims":{"technique":0.0,"composition":0.0,"content":0.0,"color":0.0},"tags":["主题","场景"],"reasons":{"strength":"优点一句话","weakness":"不足一句话（指出最影响得分的问题）"}}`
}

// rawDims 模型输出结构
type rawDims struct {
	Dims    map[string]float64 `json:"dims"`
	Tags    []string           `json:"tags"`
	Reasons struct {
		Strength string `json:"strength"`
		Weakness string `json:"weakness"`
	} `json:"reasons"`
}

var (
	jsonBlockRe = regexp.MustCompile("(?s)```json\\s*(.+?)\\s*```")
	objRe       = regexp.MustCompile(`(?s)\{.*\}`)
)

// Parse 解析模型输出为维度分；四维齐全才判定成功，越界裁剪并标记 clamped。
func Parse(content string) (store.Dims, []string, string, string, bool, error) {
	content = strings.TrimSpace(content)
	var raw rawDims
	if err := json.Unmarshal([]byte(content), &raw); err == nil && raw.Dims != nil {
		return normalize(raw)
	}
	// 优先取 ```json 代码块
	if m := jsonBlockRe.FindStringSubmatch(content); len(m) > 1 {
		if err := json.Unmarshal([]byte(m[1]), &raw); err == nil && raw.Dims != nil {
			return normalize(raw)
		}
	}
	// 再取首个花括号对象
	if m := objRe.FindString(content); m != "" {
		if err := json.Unmarshal([]byte(m), &raw); err == nil && raw.Dims != nil {
			return normalize(raw)
		}
		// 兜底：正则抽取各维度数字
		if d, ok := extractDimsByRegex(m); ok {
			return finish(d, nil, "", "", false)
		}
	}
	return store.Dims{}, nil, "", "", false, fmt.Errorf("无法从模型输出解析出完整维度分")
}

func normalize(raw rawDims) (store.Dims, []string, string, string, bool, error) {
	dims := store.Dims{}
	clamped := false
	for _, k := range dimKeys {
		v, ok := raw.Dims[k]
		if !ok {
			return store.Dims{}, nil, "", "", false, fmt.Errorf("缺少维度 %s", k)
		}
		if v < 0 || v > 10 {
			clamped = true
		}
		v = math.Min(10, math.Max(0, v))
		setDim(&dims, k, round1(v))
	}
	return finish(dims, raw.Tags, raw.Reasons.Strength, raw.Reasons.Weakness, clamped)
}

// dimRegexes 兜底正则：`"technique"\s*:\s*([0-9.]+)`
var dimRegexes = map[string]*regexp.Regexp{}

func init() {
	for _, k := range dimKeys {
		dimRegexes[k] = regexp.MustCompile(`"` + k + `"\s*:\s*([0-9]+(?:\.[0-9]+)?)`)
	}
}

func extractDimsByRegex(s string) (store.Dims, bool) {
	var d store.Dims
	for _, k := range dimKeys {
		m := dimRegexes[k].FindStringSubmatch(s)
		if len(m) < 2 {
			return store.Dims{}, false
		}
		var v float64
		fmt.Sscanf(m[1], "%f", &v)
		setDim(&d, k, round1(math.Min(10, math.Max(0, v))))
	}
	return d, true
}

func setDim(d *store.Dims, k string, v float64) {
	switch k {
	case "technique":
		d.Technique = v
	case "composition":
		d.Composition = v
	case "content":
		d.Content = v
	case "color":
		d.Color = v
	}
}

func finish(dims store.Dims, tags []string, strength, weakness string, clamped bool) (store.Dims, []string, string, string, bool, error) {
	if tags == nil {
		tags = []string{}
	}
	return dims, tags, strength, weakness, clamped, nil
}

// WeightedScore 按当前权重本地加权，四舍五入到 1 位小数
func WeightedScore(d store.Dims, w config.Weights) float64 {
	return round1(d.Technique*w.Technique + d.Composition*w.Composition + d.Content*w.Content + d.Color*w.Color)
}

// RecomputeAll 基于已存维度分批量重算总分（改权重后零成本重算）
func RecomputeAll(photos []*store.Photo, w config.Weights) map[int64]float64 {
	out := make(map[int64]float64, len(photos))
	for _, p := range photos {
		if p.Dims != nil {
			out[p.ID] = WeightedScore(*p.Dims, w)
		}
	}
	return out
}

// BucketNames 固定档位目录名
var BucketNames = []string{"35_精选", "34_良好", "33_一般", "30_待清理", "29_待复检"}

// BucketOverride 合法的手动调档取值
func BucketOverride(name string) bool {
	switch name {
	case "35_精选", "34_良好", "33_一般", "30_待清理", "29_待复检", "":
		return true
	}
	return false
}

// BucketOf 按阈值计算档位目录名；thresholds 降序（如 [9,7,5]）
func BucketOf(score float64, parseFail bool, thresholds []float64, override string) string {
	if override != "" {
		return override
	}
	if parseFail {
		return "29_待复检"
	}
	names := []string{"35_精选", "34_良好", "33_一般", "30_待清理"}
	for i, t := range thresholds {
		if score >= t && i < len(names) {
			return names[i]
		}
	}
	return "30_待清理"
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
