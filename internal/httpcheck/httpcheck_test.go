package httpcheck

import (
	"context"
	"testing"
	"time"
)

func TestCheckReachable(t *testing.T) {
	r := Check(context.Background(), "example.com", 5*time.Second)
	if !r.OK {
		t.Fatalf("example.com 应可达: %+v", r)
	}
	if r.Status <= 0 {
		t.Fatalf("状态码应>0: %+v", r)
	}
	if r.Scheme != "https" && r.Scheme != "http" {
		t.Fatalf("scheme 异常: %+v", r)
	}
}

func TestCheckUnreachable(t *testing.T) {
	r := Check(context.Background(), "digdom-no-such-host-zzz.invalid", 3*time.Second)
	if r.OK {
		t.Fatalf(".invalid 域名不应可达: %+v", r)
	}
}

func TestCheckAllOrder(t *testing.T) {
	hosts := []string{"example.com", "digdom-no-such-host-zzz.invalid", "example.com"}
	res := CheckAll(context.Background(), hosts, 5*time.Second, 10)
	if len(res) != len(hosts) {
		t.Fatalf("结果数与输入不一致: %d vs %d", len(res), len(hosts))
	}
	if !res[0].OK || res[1].OK || !res[2].OK {
		t.Fatalf("顺序/并发结果异常: %+v", res)
	}
}