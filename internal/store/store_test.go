package store

import (
	"path/filepath"
	"testing"
	"time"

	"digdom/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveAndList(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveScan(model.ScanSummary{
		Target: "example.com", Status: "done", StartedAt: time.Now().UnixMilli(), DurationMS: 100,
		Queried: 5, Hits: 2, Wildcards: 1, Unreviewed: 2,
	}, []model.Result{
		{Name: "www.example.com", Base: "example.com", Depth: 0, Tag: model.TagHit, IPs: []string{"1.2.3.4"}, CNAMEs: []string{}},
		{Name: "mail.example.com", Base: "example.com", Depth: 0, Tag: model.TagUnreviewed, IPs: nil, CNAMEs: nil},
	})
	if err != nil {
		t.Fatalf("SaveScan: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id 应为正数，got %d", id)
	}

	list, err := s.ListScans()
	if err != nil {
		t.Fatalf("ListScans: %v", err)
	}
	if len(list) != 1 || list[0].Target != "example.com" {
		t.Fatalf("列表不符: %+v", list)
	}

	rows, err := s.LoadResults(id)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("结果数应为 2，got %d", len(rows))
	}
	// 归一：nil 必须转空切片，永不为 null。
	for _, r := range rows {
		if r.IPs == nil || r.CNAMEs == nil {
			t.Fatalf("列表字段不能为 nil: %+v", r)
		}
	}
	byName := map[string]model.ResultRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	www, ok := byName["www.example.com"]
	if !ok || www.Tag != model.TagHit {
		t.Fatalf("缺 www 命中行: %+v", rows)
	}
	if www.IPs[0] != "1.2.3.4" {
		t.Fatalf("IP 不符: %+v", www.IPs)
	}
}

func TestUpdateReview(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.SaveScan(model.ScanSummary{Target: "example.com", Status: "done", StartedAt: time.Now().UnixMilli()}, []model.Result{
		{Name: "www.example.com", Tag: model.TagHit},
	})
	rows, _ := s.LoadResults(id)
	r := rows[0]

	if err := s.UpdateReview(id, r.ID, model.VerdictConfirmed, "已确认是官网"); err != nil {
		t.Fatalf("UpdateReview: %v", err)
	}

	rows, _ = s.LoadResults(id)
	if rows[0].Verdict != model.VerdictConfirmed || rows[0].Note != "已确认是官网" {
		t.Fatalf("复核未生效: %+v", rows[0])
	}
}

func TestDiffScans(t *testing.T) {
	s := newTestStore(t)
	aID, _ := s.SaveScan(model.ScanSummary{Target: "example.com", StartedAt: time.Now().UnixMilli()}, []model.Result{
		{Name: "a.example.com", Tag: model.TagHit, IPs: []string{"1.1.1.1"}},
		{Name: "b.example.com", Tag: model.TagHit},
		{Name: "c.example.com", Tag: model.TagHit},
	})
	bID, _ := s.SaveScan(model.ScanSummary{Target: "example.com", StartedAt: time.Now().UnixMilli()}, []model.Result{
		{Name: "b.example.com", Tag: model.TagHit},
		{Name: "d.example.com", Tag: model.TagHit},
	})

	d, err := s.DiffScans(aID, bID)
	if err != nil {
		t.Fatalf("DiffScans: %v", err)
	}
	added := map[string]bool{}
	for _, i := range d.Added {
		added[i.Name] = true
	}
	if !added["d.example.com"] {
		t.Fatalf("新增应含 d.example.com: %+v", d.Added)
	}
	removed := map[string]bool{}
	for _, i := range d.Removed {
		removed[i.Name] = true
	}
	if !removed["a.example.com"] || !removed["c.example.com"] {
		t.Fatalf("消失应含 a,c: %+v", d.Removed)
	}
	if len(d.Added)+len(d.Removed) != 3 {
		t.Fatalf("差异总数应为 3，got %d", len(d.Added)+len(d.Removed))
	}
}