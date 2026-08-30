// Package config 负责配置的加载、持久化与默认值兜底。
// 配置文件位于用户目录（%LOCALAPPDATA%\SnapRank\config.yaml），不写应用安装目录。
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Weights 评分维度权重（总分 = Σ 维度分×权重，本地计算）
type Weights struct {
	Technique   float64 `yaml:"technique" json:"technique"`     // 技术质量
	Composition float64 `yaml:"composition" json:"composition"` // 构图
	Content     float64 `yaml:"content" json:"content"`         // 内容与情感
	Color       float64 `yaml:"color" json:"color"`             // 色彩
}

// ModelPrice 模型单价（元/百万 tokens），用于成本预估
type ModelPrice struct {
	InputPerM  float64 `yaml:"input" json:"input"`
	OutputPerM float64 `yaml:"output" json:"output"`
}

// Provider 平台接入配置
type Provider struct {
	Type    string `yaml:"type" json:"type"`         // tokenrhythm(OpenAI兼容) | mock
	BaseURL string `yaml:"base_url" json:"base_url"` // 平台 API 地址
	APIKey  string `yaml:"api_key" json:"api_key"`
}

// ModelConfig 模型相关配置
type ModelConfig struct {
	Default        string   `yaml:"default" json:"default"`                 // 默认打分模型
	VisionPatterns []string `yaml:"vision_patterns" json:"vision_patterns"` // 视觉模型识别正则（/v1/models 无模态元数据）
}

// ScoreConfig 评分参数
type ScoreConfig struct {
	Temperature float32   `yaml:"temperature" json:"temperature"`
	MaxTokens   int       `yaml:"max_tokens" json:"max_tokens"`
	TimeoutSec  int       `yaml:"timeout_sec" json:"timeout_sec"`
	Thresholds  []float64 `yaml:"thresholds" json:"thresholds"`     // 分档阈值，降序，默认 [9,7,5]
	ReuseScores bool      `yaml:"reuse_scores" json:"reuse_scores"` // 跨会话指纹缓存复用评分（0 计费）
}

// PipelineConfig 流水线参数
type PipelineConfig struct {
	ScoreConcurrency    int `yaml:"score_concurrency" json:"score_concurrency"`       // 评分并发
	CompressConcurrency int `yaml:"compress_concurrency" json:"compress_concurrency"` // 压缩并发
	MaxEdge             int `yaml:"max_edge" json:"max_edge"`                         // 压缩图最长边
	JPEGQuality         int `yaml:"jpeg_quality" json:"jpeg_quality"`                 // DD鹅 MozJPEG 质量
	MinFileSizeKB       int `yaml:"min_file_size_kb" json:"min_file_size_kb"`         // 小于该体积视为缩略图，跳过
}

// CostConfig 成本护栏
type CostConfig struct {
	BatchLimit float64               `yaml:"batch_limit" json:"batch_limit"` // 单批次预估费用上限（元），0=不限
	DailyLimit float64               `yaml:"daily_limit" json:"daily_limit"` // 每日累计预估上限（元），0=不限
	Prices     map[string]ModelPrice `yaml:"prices" json:"prices"`           // 模型单价表
}

// PathsConfig 路径配置
type PathsConfig struct {
	ArchiveRoot string `yaml:"archive_root" json:"archive_root"` // 归档输出根目录
	LibDir      string `yaml:"lib_dir" json:"lib_dir"`           // DD鹅 lib 工具目录
	DataDir     string `yaml:"data_dir" json:"data_dir"`         // 数据目录（配置/库/缓存/日志父目录）
}

// Config 全量配置
type Config struct {
	Provider Provider       `yaml:"provider" json:"provider"`
	Model    ModelConfig    `yaml:"model" json:"model"`
	Weights  Weights        `yaml:"weights" json:"weights"`
	Score    ScoreConfig    `yaml:"score" json:"score"`
	Pipeline PipelineConfig `yaml:"pipeline" json:"pipeline"`
	Cost     CostConfig     `yaml:"cost" json:"cost"`
	Paths    PathsConfig    `yaml:"paths" json:"paths"`
}

// DefaultDataDir 返回默认数据目录（%LOCALAPPDATA%\SnapRank）
func DefaultDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "SnapRank")
}

// DefaultPicturesDir 返回默认归档根目录（%USERPROFILE%\Pictures\SnapRank）
func DefaultPicturesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "archive")
	}
	return filepath.Join(home, "Pictures", "SnapRank")
}

// Default 返回带完整默认值的配置
func Default() *Config {
	dataDir := DefaultDataDir()
	return &Config{
		Provider: Provider{
			Type:    "tokenrhythm",
			BaseURL: "https://api.tokenrhythm.studio/v1",
			APIKey:  "",
		},
		Model: ModelConfig{
			Default: "qwen3.7-flash",
			VisionPatterns: []string{
				`qwen.*v|qwen.*flash|qwen.*vl`,
				`glm-?[45].*v|glm-5|glm-4v`,
				`seed-2|doubao.*vision|vision`,
				`internvl|llava|gemma-3|step-1v|yi-vision`,
			},
		},
		Weights: Weights{Technique: 0.4, Composition: 0.3, Content: 0.2, Color: 0.1},
		Score: ScoreConfig{
			Temperature: 0.2,
			MaxTokens:   512,
			TimeoutSec:  60,
			Thresholds:  []float64{9, 7, 5},
			ReuseScores: true,
		},
		Pipeline: PipelineConfig{
			ScoreConcurrency:    4,
			CompressConcurrency: 2,
			MaxEdge:             2048,
			JPEGQuality:         82,
			MinFileSizeKB:       10,
		},
		Cost: CostConfig{
			BatchLimit: 10,
			DailyLimit: 20,
			Prices: map[string]ModelPrice{
				"qwen3.7-flash":  {InputPerM: 1.20, OutputPerM: 4.00},
				"glm-5.3-flash":  {InputPerM: 0.40, OutputPerM: 1.60},
				"qwen3.8-27b":    {InputPerM: 3.00, OutputPerM: 9.00},
				"seed-2.1-turbo": {InputPerM: 3.00, OutputPerM: 9.00},
				"seed-2.1-pro":   {InputPerM: 6.00, OutputPerM: 18.00},
			},
		},
		Paths: PathsConfig{
			ArchiveRoot: DefaultPicturesDir(),
			LibDir:      `D:\wwwroot\wwwroot\ddGoose-go\lib`,
			DataDir:     dataDir,
		},
	}
}

// Path 配置文件路径
func Path(dataDir string) string {
	return filepath.Join(dataDir, "config.yaml")
}

// Load 读取配置；文件不存在或字段缺省时以默认值兜底
func Load(dataDir string) (*Config, error) {
	cfg := Default()
	cfg.Paths.DataDir = dataDir
	p := Path(dataDir)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.Paths.DataDir = dataDir
	cfg.normalize()
	return cfg, nil
}

// Save 持久化配置到用户目录
func (c *Config) Save() error {
	if err := os.MkdirAll(c.Paths.DataDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(c.Paths.DataDir), data, 0o600)
}

// normalize 兜底非法取值
func (c *Config) normalize() {
	d := Default()
	if c.Provider.Type == "" {
		c.Provider.Type = d.Provider.Type
	}
	if c.Provider.BaseURL == "" {
		c.Provider.BaseURL = d.Provider.BaseURL
	}
	if c.Score.Temperature <= 0 {
		c.Score.Temperature = d.Score.Temperature
	}
	if c.Score.MaxTokens <= 0 {
		c.Score.MaxTokens = d.Score.MaxTokens
	}
	if c.Score.TimeoutSec <= 0 {
		c.Score.TimeoutSec = d.Score.TimeoutSec
	}
	if len(c.Score.Thresholds) != 3 {
		c.Score.Thresholds = d.Score.Thresholds
	}
	for i := 1; i < len(c.Score.Thresholds); i++ {
		if c.Score.Thresholds[i] > c.Score.Thresholds[i-1] {
			c.Score.Thresholds[i] = c.Score.Thresholds[i-1]
		}
	}
	if c.Pipeline.ScoreConcurrency <= 0 || c.Pipeline.ScoreConcurrency > 16 {
		c.Pipeline.ScoreConcurrency = d.Pipeline.ScoreConcurrency
	}
	if c.Pipeline.CompressConcurrency <= 0 || c.Pipeline.CompressConcurrency > 16 {
		c.Pipeline.CompressConcurrency = d.Pipeline.CompressConcurrency
	}
	if c.Pipeline.MaxEdge < 512 || c.Pipeline.MaxEdge > 8192 {
		c.Pipeline.MaxEdge = d.Pipeline.MaxEdge
	}
	if c.Pipeline.JPEGQuality < 40 || c.Pipeline.JPEGQuality > 100 {
		c.Pipeline.JPEGQuality = d.Pipeline.JPEGQuality
	}
	if c.Pipeline.MinFileSizeKB < 0 {
		c.Pipeline.MinFileSizeKB = d.Pipeline.MinFileSizeKB
	}
	if c.Paths.ArchiveRoot == "" {
		c.Paths.ArchiveRoot = d.Paths.ArchiveRoot
	}
	if c.Paths.LibDir == "" {
		c.Paths.LibDir = d.Paths.LibDir
	}
	if c.Paths.DataDir == "" {
		c.Paths.DataDir = d.Paths.DataDir
	}
	if c.Model.Default == "" {
		c.Model.Default = d.Model.Default
	}
	// 权重归一：全为 0 时回默认；总和为 0 时均分
	w := c.Weights
	if w.Technique <= 0 && w.Composition <= 0 && w.Content <= 0 && w.Color <= 0 {
		c.Weights = d.Weights
	}
}

// WeightsNormalized 返回归一化后的权重（和为 1）
func (c *Config) WeightsNormalized() Weights {
	sum := c.Weights.Technique + c.Weights.Composition + c.Weights.Content + c.Weights.Color
	if sum <= 0 {
		return Default().Weights
	}
	return Weights{
		Technique:   c.Weights.Technique / sum,
		Composition: c.Weights.Composition / sum,
		Content:     c.Weights.Content / sum,
		Color:       c.Weights.Color / sum,
	}
}
