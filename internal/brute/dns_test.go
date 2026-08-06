package brute

import (
	"context"
	"testing"
	"time"
)

func TestResolveDebug(t *testing.T) {
	r := NewResolver(nil, 2*time.Second, 2)
	msg, err := r.Resolve(context.Background(), "www.example.com")
	t.Logf("direct err=%v", err)
	if msg != nil {
		ips, cn := ParseAnswer(msg)
		t.Logf("direct rcode=%d ips=%v cnames=%v", msg.Rcode, ips, cn)
	}
}
