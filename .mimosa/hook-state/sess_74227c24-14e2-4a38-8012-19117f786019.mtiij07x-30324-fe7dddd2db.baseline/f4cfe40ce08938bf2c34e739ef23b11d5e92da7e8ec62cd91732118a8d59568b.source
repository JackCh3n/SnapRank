package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSessionAndPhotoLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.CreateSession("s1", "D:\\photos", "mock-scorer", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindResumableSession("D:\\photos"); err != nil {
		t.Fatal("应找到可续跑会话")
	}

	id, err := s.UpsertPhoto(&Photo{SessionID: "s1", Fingerprint: "fp1", SrcPath: "D:\\photos\\a.jpg",
		Filename: "a.jpg", RelPath: "a.jpg", Size: 10, Status: StatusPending})
	if err != nil {
		t.Fatal(err)
	}
	// 同路径重复 Upsert 不产生新行
	id2, err := s.UpsertPhoto(&Photo{SessionID: "s1", Fingerprint: "fp1", SrcPath: "D:\\photos\\a.jpg",
		Filename: "a.jpg", RelPath: "a.jpg", Size: 10, Status: StatusPending})
	if err != nil || id != id2 {
		t.Fatalf("同 (session,path) 应复用同一行: %d vs %d (%v)", id, id2, err)
	}

	s.SetPhotoCompressed(id, "cache/fp1.jpg")
	dims := Dims{Technique: 8, Composition: 7, Content: 6, Color: 5}
	if err := s.SetPhotoResult(id, StatusScored, 6.9, &dims, []string{"风光"}, "好", "差", "mock-scorer", "v2", "api", false, 123); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPhoto(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusScored || p.Score != 6.9 || p.Dims == nil || p.Dims.Composition != 7 || len(p.Tags) != 1 {
		t.Fatalf("结果落库不符: %+v", p)
	}

	counts, _ := s.SessionStatusCounts("s1")
	if counts[StatusScored] != 1 {
		t.Fatalf("状态计数不符: %v", counts)
	}
	list, total, _ := s.ListPhotos("s1", StatusScored, 0, 10, "score", "desc", "", "", -1, -1)
	if total != 1 || len(list) != 1 {
		t.Fatalf("分页查询不符: total=%d", total)
	}
}

func TestScoreCache(t *testing.T) {
	s := openTest(t)
	if _, err := s.CacheGet("fp", "m", "v2"); err != ErrNotFound {
		t.Fatalf("未命中应 ErrNotFound，got %v", err)
	}
	dims := Dims{Technique: 1, Composition: 2, Content: 3, Color: 4}
	if err := s.CachePut("fp", "m", "v2", dims, []string{"t"}, "s", "w"); err != nil {
		t.Fatal(err)
	}
	e, err := s.CacheGet("fp", "m", "v2")
	if err != nil || e.Dims != dims || len(e.Tags) != 1 {
		t.Fatalf("缓存读写不符: %+v %v", e, err)
	}
	// 模型/版本隔离
	if _, err := s.CacheGet("fp", "other", "v2"); err != ErrNotFound {
		t.Fatal("不同模型不应命中")
	}
}

func TestSpendLog(t *testing.T) {
	s := openTest(t)
	if v, _ := s.SpendToday("2026-08-30"); v != 0 {
		t.Fatalf("初始应为 0，got %v", v)
	}
	s.SpendAdd("2026-08-30", "m", 10, 0.5)
	s.SpendAdd("2026-08-30", "m", 5, 0.25)
	s.SpendAdd("2026-08-29", "m", 5, 99)
	if v, _ := s.SpendToday("2026-08-30"); v != 0.75 {
		t.Fatalf("当日累计应为 0.75，got %v", v)
	}
}

func TestResetPhoto(t *testing.T) {
	s := openTest(t)
	s.CreateSession("s1", "d", "m", "v2")
	id, _ := s.UpsertPhoto(&Photo{SessionID: "s1", Fingerprint: "old", SrcPath: "p", Filename: "p", Status: StatusPending})
	dims := Dims{Technique: 1, Composition: 1, Content: 1, Color: 1}
	s.SetPhotoResult(id, StatusScored, 5, &dims, nil, "", "", "m", "v2", "api", false, 10)
	s.SetPhotoBucket(id, "35_精选")
	if err := s.ResetPhoto(id); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetPhoto(id)
	if p.Status != StatusPending || p.Score != 0 || p.Dims != nil || p.OverrideBucket != "35_精选" {
		t.Fatalf("重置结果不符: %+v", p)
	}
}
