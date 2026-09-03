// Package provider 平台接入抽象：主力为基元律动（OpenAI 兼容协议），
// 内置 mock 离线实现（确定性伪评分，0 成本，供演示/测试/无 Key 体验）。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"snaprank/internal/config"
	"snaprank/internal/logutil"
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
	Effort    string // 思考强度: ""=模型默认 | low | medium | high（仅思考型模型生效）
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
		if cfg.Provider.Protocol == "anthropic" {
			return &AnthropicProvider{cfg: cfg}, nil
		}
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
	start := time.Now()
	logutil.Info("[评分] %s 开始（超时 %ds）", model, int(req.Timeout.Seconds()))

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
		if req.Effort != "" {
			r.ReasoningEffort = req.Effort
		}
		resp, err := t.client.CreateChatCompletion(ctx, r)
		if err != nil {
			if strings.Contains(err.Error(), "context deadline exceeded") {
				return "", fmt.Errorf("请求超时（%ds）：模型响应慢或图片过大，可在设置增大超时/降低并发，或换更快模型", int(req.Timeout.Seconds()))
			}
			return "", classifyErr(err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("模型无返回内容")
		}
		content := resp.Choices[0].Message.Content
		logutil.Info("[评分] %s 完成，耗时 %dms", model, time.Since(start).Milliseconds())
		// 思考型模型可能把 max_tokens 全部用于推理，正文被截断为空
		if strings.TrimSpace(content) == "" && resp.Choices[0].FinishReason == openai.FinishReasonLength {
			return "", fmt.Errorf("模型输出为空：max_tokens(%d) 被推理过程耗尽（finish_reason=length），请在设置中增大 max_tokens", req.MaxTokens)
		}
		return content, nil
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

// ---------- Anthropic Messages（/v1/messages） ----------

// AnthropicProvider Anthropic Messages 协议实现（/v1/messages）。
// 适用于 Anthropic 官方 API 及兼容该协议的网关（如火山 coding plan）。
type AnthropicProvider struct {
	cfg *config.Config
}

// Name 平台名
func (a *AnthropicProvider) Name() string { return "anthropic" }

// anthropicResp /v1/messages 响应
type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthropicHTTPClient 强制 HTTP/1.1：火山网关的 WAF 对 HTTP/2 请求
// 会返回误导性的 AuthHeader 400，降级 1.1 后正常
var anthropicHTTPClient = &http.Client{}

func init() {
	// 空 Transport 默认不启用 HTTP/2（ForceAttemptHTTP2 只在 DefaultClient 生效）
	anthropicHTTPClient.Transport = &http.Transport{}
}

// setAuth 按平台设置认证头：火山只认 Bearer，官方认 x-api-key
func (a *AnthropicProvider) setAuth(req *http.Request) {
	if a.isVolcano() {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Provider.APIKey)
	} else {
		req.Header.Set("x-api-key", a.cfg.Provider.APIKey)
	}
}

// isVolcano 是否火山 anthropic 兼容网关（认证方式与模型目录与官方不同）
func (a *AnthropicProvider) isVolcano() bool {
	return strings.Contains(a.cfg.Provider.BaseURL, "ark.cn-beijing.volces.com") ||
		strings.Contains(a.cfg.Provider.BaseURL, "/api/coding")
}

// volcanoCodingPlanModels 火山 coding plan 实测支持的视觉模型精选
// （网关 /v1/models 返回全量 ark 目录 130+，其中大量模型不在 coding plan
//
//	内、调用会报 UnsupportedModel；该清单来自实测排查）
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
func (a *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	base := strings.TrimRight(a.cfg.Provider.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if a.isVolcano() {
		// 火山 anthropic 网关只认 Bearer（x-api-key 与 Authorization 并存会报 AuthHeader 冲突）
		req.Header.Set("Authorization", "Bearer "+a.cfg.Provider.APIKey)
		logutil.Info("[models] 火山分支: Bearer 认证")
	} else {
		req.Header.Set("x-api-key", a.cfg.Provider.APIKey)
		logutil.Info("[models] 官方分支: x-api-key 认证")
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	logutil.Info("[models] url=%s 协议=%s", base+"/models", a.cfg.Provider.Protocol)
	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	logutil.Info("[models] 网关返回 %d 模型", len(ids))
	// 火山 coding plan 网关的 /v1/models 返回全量 ark 目录（含大量不在
	// coding plan 内的模型，调用会报 UnsupportedModel）；按实测清单过滤
	if a.isVolcano() {
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
			logutil.Info("[models] coding plan 过滤后 %d", len(filtered))
			return filtered, nil
		}
		logutil.Info("[models] 过滤后为 0，退回全量 %d", len(ids))
	}
	return ids, nil
}

// Score 调用 Anthropic Messages 协议评分
func (a *AnthropicProvider) Score(ctx context.Context, model string, req ScoreRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	base := strings.TrimRight(a.cfg.Provider.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	payload := map[string]interface{}{
		"model":      model,
		"max_tokens": req.MaxTokens,
		"messages": []map[string]interface{}{{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "image", "source": map[string]string{
					"type": "base64", "media_type": "image/jpeg", "data": req.ImageB64,
				}},
				{"type": "text", "text": req.Prompt},
			},
		}},
	}
	body, _ := json.Marshal(payload)
	req2, err := http.NewRequestWithContext(ctx, "POST", base+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req2.Header.Set("content-type", "application/json")
	a.setAuth(req2)
	req2.Header.Set("anthropic-version", "2023-06-01")
	resp, err := anthropicHTTPClient.Do(req2)
	if err != nil {
		return "", classifyErr(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("messages %d: %s", resp.StatusCode, truncateStr(string(raw), 200))
	}
	var out anthropicResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic: %s", out.Error.Message)
	}
	// 拼接 text 块（思考型模型可能夹带 thinking 块，跳过）
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	content := sb.String()
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("模型输出为空（可能 max_tokens 被推理耗尽），请增大 max_tokens")
	}
	return content, nil
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
		v := float64((x>>salt)%100) / 100.0 // 0~1
		return base + v*spread
	}
	dims := map[string]float64{
		"technique":   math.Min(10, pick(4.0, 5.5, 0)),
		"composition": math.Min(10, pick(4.0, 5.0, 8)),
		"content":     math.Min(10, pick(3.5, 5.5, 16)),
		"color":       math.Min(10, pick(4.5, 5.0, 24)),
	}
	b, _ := json.Marshal(map[string]interface{}{
		"dims": dims,
		"tags": mockTags(x),
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
