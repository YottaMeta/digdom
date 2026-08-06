package brute

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/miekg/dns"
)

// WildcardInfo 某个层级的泛解析探测结果。
type WildcardInfo struct {
	Detected bool     // 该层级是否存在泛解析
	IPs      []string // 泛解析返回的 IP 集合
}

// nonce 生成随机前缀，避免撞上真实记录。
func nonce(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strings.ToLower(prefix + "deadbeef")
	}
	return strings.ToLower(prefix + hex.EncodeToString(b))
}

// DetectWildcard 探测 base 层级是否存在泛解析：
// 用随机 nonce 子域解析，返回非 NXDOMAIN 即视为存在。
func DetectWildcard(ctx context.Context, r *Resolver, base string) *WildcardInfo {
	n := nonce("gz")
	msg, err := r.Resolve(ctx, n+"."+base)
	if err != nil {
		return &WildcardInfo{}
	}
	if IsNXDOMAIN(msg) || msg.Rcode == dns.RcodeServerFailure {
		return &WildcardInfo{}
	}
	ips, _ := ParseAnswer(msg)
	return &WildcardInfo{Detected: true, IPs: ips}
}
