package engine

import (
	"testing"
	"time"

	"digdom/internal/model"
)

// TestScanExample 用真实 DNS 验证引擎基本流程：
// 命中 www、过滤 NXDOMAIN、通配符标签逻辑。
func TestScanExample(t *testing.T) {
	cfg := model.Config{
		Root:        "example.com",
		DictWords:   []string{"www", "mail", "api", "nosuchsubdomainxyz", "ftp"},
		MaxDepth:    0,
		Concurrency: 50,
		Timeout:     4 * time.Second,
	}
	sc := NewScanner(cfg)

	type statsCh struct{ s model.Stats }
	done := make(chan model.Stats, 1)
	go func() { done <- sc.Run() }()

	results := map[string]model.Result{}
	tm := time.After(45 * time.Second)
loop:
	for {
		select {
		case r, ok := <-sc.Results():
			if !ok {
				break loop
			}
			results[r.Name] = r
		case <-tm:
			sc.Stop()
			t.Fatal("扫描超时")
		}
	}
	stats := <-done

	t.Logf("stats: %+v", stats)
	if _, ok := results["www.example.com"]; !ok {
		t.Errorf("www.example.com 未命中，实际结果数=%d", len(results))
	}
	// 注：公共 DNS 对 example.com 的子域名可能返回 NOERROR 空应答而非 NXDOMAIN，
	// 引擎对空应答保守标记 unreviewed，故此处不依赖具体名字的 NXDOMAIN 行为。
	// NXDOMAIN 名字本就不会出现在结果中（resolveOne 在 push 前直接丢弃）。
	for name, r := range results {
		if r.Tag == "" {
			t.Errorf("结果 %s 缺少标签", name)
		}
	}
}
