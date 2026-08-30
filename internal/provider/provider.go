// Package provider 平台接入抽象：主力为基元律动（OpenAI 兼容协议），
// 内置 mock 离线实现（确定性伪评分，0 成本，供演示/测试/无 Key 体验）。
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"snaprank/internal/config"
)

// ErrRateLimit 平台限流（429），由流水线动态降并发
var ErrRateLimit = errors.New("rate limited (429)")

// ErrModelNoVision 模型不支持图片输入
var ErrModelNoVision = errors.New("model does not support image input")

// ScoreRequest 单次评分请求
type ScoreRequest struct {
	ImageB64  string // data URI 前缀由 Provider 拼接
	Filename  string // 原文件名（mock 用作确定性触发，仅本地使用不上传）
	Prompt    string
	Temp      float32
	MaxTokens int
	Timeout   time.Duration
}

// Provider 平台抽象
type Provider interface {
	Name() string
	// ListModels 返回全部可用模型 ID
	ListModels(ctx context.Context) ([]string, error)
	// Score 调用模型返回原始文本输出（由 scorer 解析）
	Score(ctx context.Context, model string, req ScoreRequest) (string, error)
}

// New 按 type 构造 Provider
func New(cfg *config.Config) (Provider, error) {
	switch cfg.Provider.Type {
	case "mock":
		return &Mock{}, nil
	default:
		return &TokenRhythm{cfg: cfg, client: newClient(cfg)}, nil
	}
}

func newClient(cfg *config.Config) *openai.Client {
	cc := openai.DefaultConfig(cfg.Provider.APIKey)
	cc.BaseURL = strings.TrimRight(cfg.Provider.BaseURL, "/")
	return openai.NewClientWithConfig(cc)
}

// ---------- 基元律动（OpenAI 兼容） ----------

// TokenRhythm OpenAI 兼容聚合平台实现
type TokenRhythm struct {
	cfg    *config.Config
	client *openai.Client
}

// Name 平台名
func (t *TokenRhythm) Name() string { return "tokenrhythm" }

// ListModels 拉取平台模型清单
func (t *TokenRhythm) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ml, err := t.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ml.Models))
	for _, m := range ml.Models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// FilterVision 按内置正则白名单过滤视觉模型（/v1/models 无模态元数据）
func FilterVision(ids []string, patterns []string) []string {
	if len(patterns) == 0 {
		return ids
	}
	var res []string
	for _, id := range ids {
		for _, p := range patterns {
			re, err := regexp.Compile("(?i)" + p)
			if err != nil {
				continue
			}
			if re.MatchString(id) {
				res = append(res, id)
				break
			}
		}
	}
	return res
}

// Score 调用视觉模型；带 json_object 模式，报错自动降级重试一次
func (t *TokenRhythm) Score(ctx context.Context, model string, req ScoreRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	textPart := openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: req.Prompt}
	imgPart := openai.ChatMessagePart{Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{URL: "data:image/jpeg;base64," + req.ImageB64}}

	call := func(jsonMode bool) (string, error) {
		r := openai.ChatCompletionRequest{
			Model:       model,
			Temperature: req.Temp,
			MaxTokens:   req.MaxTokens,
			Messages: []openai.ChatCompletionMessage{{
				Role:         "user",
				MultiContent: []openai.ChatMessagePart{textPart, imgPart},
			}},
		}
		if jsonMode {
			r.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
		}
		resp, err := t.client.CreateChatCompletion(ctx, r)
		if err != nil {
			return "", classifyErr(err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("模型无返回内容")
		}
		return resp.Choices[0].Message.Content, nil
	}

	out, err := call(true)
	if err != nil && isFormatRelated(err) {
		out, err = call(false)
	}
	return out, err
}

func isFormatRelated(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "response_format") || strings.Contains(msg, "400")
}

func classifyErr(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatusCode == 429 {
			return fmt.Errorf("%w: %s", ErrRateLimit, apiErr.Message)
		}
		if apiErr.HTTPStatusCode == 400 && (strings.Contains(apiErr.Message, "image") || strings.Contains(apiErr.Message, "多模态") || strings.Contains(apiErr.Message, "vision")) {
			return fmt.Errorf("%w: %s", ErrModelNoVision, apiErr.Message)
		}
	}
	return err
}

// ---------- mock（离线演示/测试） ----------

// Mock 确定性伪评分：基于图片内容哈希生成稳定分数；
// 文件名包含 BADJSON 时返回畸形输出（验证 parse_fail 链路）。
type Mock struct{}

// Name 平台名
func (m *Mock) Name() string { return "mock" }

// ListModels 返回固定清单
func (m *Mock) ListModels(ctx context.Context) ([]string, error) {
	return []string{"mock-scorer", "mock-strict"}, nil
}

// Score 生成确定性伪评分输出
func (m *Mock) Score(ctx context.Context, model string, req ScoreRequest) (string, error) {
	if strings.Contains(strings.ToUpper(req.Filename), "BADJSON") {
		return `哎呀，这张照片感觉 {一般吧}`, nil
	}
	h := fnv.New64a()
	h.Write([]byte(req.ImageB64[:min(2048, len(req.ImageB64))]))
	x := h.Sum64()

	pick := func(base, spread float64, salt uint64) float64 {
		v := float64((x>>salt)%100)/100.0 // 0~1
		return base + v*spread
	}
	dims := map[string]float64{
		"technique":   math.Min(10, pick(4.0, 5.5, 0)),
		"composition": math.Min(10, pick(4.0, 5.0, 8)),
		"content":     math.Min(10, pick(3.5, 5.5, 16)),
		"color":       math.Min(10, pick(4.5, 5.0, 24)),
	}
	b, _ := json.Marshal(map[string]interface{}{
		"dims":  dims,
		"tags":  mockTags(x),
		"reasons": map[string]string{
			"strength": "（mock）画面整体完整，主体明确",
			"weakness": "（mock）为离线演示生成的确定性评分",
		},
	})
	time.Sleep(150 * time.Millisecond) // 模拟网络延迟，便于观察并发
	return string(b), nil
}

func mockTags(x uint64) []string {
	all := [][]string{
		{"风光", "户外"}, {"人像", "室内"}, {"街拍", "城市"}, {"美食", "静物"}, {"夜景", "灯光"}, {"纪实", "生活"},
	}
	return all[x%uint64(len(all))]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
