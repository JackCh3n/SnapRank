package scorer

import (
	"testing"

	"snaprank/internal/config"
	"snaprank/internal/store"
)

func TestParseCleanJSON(t *testing.T) {
	in := `{"dims":{"technique":8.5,"composition":7,"content":9.2,"color":6.8},"tags":["风光"],"reasons":{"strength":"好","weakness":"差"}}`
	dims, tags, s, w, clamped, err := Parse(in)
	if err != nil {
		t.Fatalf("应解析成功: %v", err)
	}
	if dims.Technique != 8.5 || dims.Composition != 7 || dims.Content != 9.2 || dims.Color != 6.8 {
		t.Fatalf("维度分不符: %+v", dims)
	}
	if len(tags) != 1 || tags[0] != "风光" || s != "好" || w != "差" || clamped {
		t.Fatalf("其余字段不符: %v %s %s %v", tags, s, w, clamped)
	}
}

func TestParseCodeBlock(t *testing.T) {
	in := "评审如下：\n```json\n{\"dims\":{\"technique\":1,\"composition\":2,\"content\":3,\"color\":4},\"tags\":[]}\n```"
	if _, _, _, _, _, err := Parse(in); err != nil {
		t.Fatalf("代码块应解析成功: %v", err)
	}
}

func TestParseMissingDimFails(t *testing.T) {
	// 缺 color → 判定失败（parse_fail 链路）
	in := `{"dims":{"technique":5,"composition":5,"content":5},"tags":[]}`
	if _, _, _, _, _, err := Parse(in); err == nil {
		t.Fatal("缺少维度应报错")
	}
}

func TestParseClampOutOfRange(t *testing.T) {
	in := `{"dims":{"technique":12,"composition":-3,"content":5,"color":5},"tags":[]}`
	_, _, _, _, clamped, err := Parse(in)
	if err != nil || !clamped {
		t.Fatalf("越界应裁剪并标记 clamped: %v %v", clamped, err)
	}
}

func TestParseGarbageFails(t *testing.T) {
	if _, _, _, _, _, err := Parse("哎呀这张一般吧"); err == nil {
		t.Fatal("纯文本应解析失败")
	}
}

func TestParseRegexFallback(t *testing.T) {
	in := `说明 {"technique": 7.2, "composition": 6, "content": 8, "color": 5} 完毕`
	if _, _, _, _, _, err := Parse(in); err != nil {
		t.Fatalf("正则兜底应成功: %v", err)
	}
}

func TestWeightedScore(t *testing.T) {
	w := config.Weights{Technique: 0.4, Composition: 0.3, Content: 0.2, Color: 0.1}
	d := store.Dims{Technique: 10, Composition: 10, Content: 10, Color: 10}
	if got := WeightedScore(d, w); got != 10 {
		t.Fatalf("全 10 应得 10，got %v", got)
	}
	d2 := store.Dims{Technique: 8, Composition: 6, Content: 7, Color: 5}
	// 8*.4+6*.3+7*.2+5*.1 = 3.2+1.8+1.4+0.5 = 6.9
	if got := WeightedScore(d2, w); got != 6.9 {
		t.Fatalf("加权应得 6.9，got %v", got)
	}
}

func TestBucketOf(t *testing.T) {
	th := []float64{9, 7, 5}
	cases := []struct {
		score    float64
		fail     bool
		override string
		want     string
	}{
		{9.0, false, "", "35_精选"},
		{8.9, false, "", "34_良好"},
		{7.0, false, "", "34_良好"},
		{6.9, false, "", "33_一般"},
		{5.0, false, "", "33_一般"},
		{4.9, false, "", "30_待清理"},
		{9.5, true, "", "29_待复检"},      // parse_fail 优先于分数
		{4.0, false, "35_精选", "35_精选"}, // 手动调档优先
	}
	for _, c := range cases {
		if got := BucketOf(c.score, c.fail, th, c.override); got != c.want {
			t.Errorf("BucketOf(%v,%v,%q) = %q, want %q", c.score, c.fail, c.override, got, c.want)
		}
	}
}
