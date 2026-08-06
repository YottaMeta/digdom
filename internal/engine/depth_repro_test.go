package engine

import (
	"runtime/debug"
	"testing"
	"time"

	"digdom/internal/model"
)

// TestScanDepth1 复现"多层递归崩溃"：example.com 深度 1。
func TestScanDepth1(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC: %v\n%s", r, debug.Stack())
		}
	}()

	cfg := model.Config{
		Root:        "example.com",
		DictWords:   []string{"www", "mail", "api", "nosuchsubdomainxyz", "ftp"},
		MaxDepth:    1,
		Concurrency: 50,
		Timeout:     4 * time.Second,
	}
	sc := NewScanner(cfg)

	done := make(chan model.Stats, 1)
	go func() { done <- sc.Run() }()

	count := 0
	tm := time.After(60 * time.Second)
loop:
	for {
		select {
		case r, ok := <-sc.Results():
			if !ok {
				break loop
			}
			count++
			t.Logf("结果: %s tag=%s depth=%d", r.Name, r.Tag, r.Depth)
		case <-tm:
			sc.Stop()
			t.Fatal("扫描超时（疑似死锁）")
		}
	}
	stats := <-done
	t.Logf("完成 stats=%+v 结果数=%d", stats, count)
}
