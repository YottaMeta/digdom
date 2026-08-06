package brute

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Resolver 基于 miekg/dns 的容错解析器：
// 多 DNS 轮询、UDP 失败/截断自动切 TCP、失败换服务器重试。
// UDP 若在首个查询即失败，会标记 tcpOnly 自愈，后续直走 TCP，避免反复等 UDP 超时。
type Resolver struct {
	servers []string
	timeout time.Duration
	retries int
	base    uint64
	tcpOnly atomic.Bool
}

// DefaultServers 内置公共 DNS 池。
var DefaultServers = []string{
	"8.8.8.8",
	"1.1.1.1",
	"223.5.5.5",
	"114.114.114.114",
}

// NewResolver 构造解析器。servers 为空时使用默认池。
func NewResolver(servers []string, timeout time.Duration, retries int) *Resolver {
	if len(servers) == 0 {
		servers = DefaultServers
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if retries < 0 {
		retries = 0
	}
	return &Resolver{servers: servers, timeout: timeout, retries: retries}
}

// serverAt 按调用次数轮转，并在重试时顺序换服务器。地址统一带 :53 端口。
func (r *Resolver) serverAt(attempt int) string {
	n := len(r.servers)
	if n == 0 {
		return "8.8.8.8:53"
	}
	off := atomic.AddUint64(&r.base, 1)
	host := r.servers[(int(off)+attempt)%n]
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "53")
}

// Resolve 解析 A 记录（自动包含 AAAA/CNAME 走 Answer 解析由调用方处理）。
// 返回 *dns.Msg，调用方可配合 ParseAnswer / IsNXDOMAIN 使用。
func (r *Resolver) Resolve(ctx context.Context, name string) (*dns.Msg, error) {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), dns.TypeA)

	var lastErr error
	for attempt := 0; attempt <= r.retries; attempt++ {
		server := r.serverAt(attempt)

		if !r.tcpOnly.Load() {
			udp := &dns.Client{Net: "udp", Timeout: r.timeout, UDPSize: 4096}
			resp, _, err := udp.ExchangeContext(ctx, q, server)
			if err == nil && !resp.Truncated {
				return resp, nil
			}
			// UDP 失败或截断 → 标记并走 TCP 兜底
			r.tcpOnly.Store(true)
		}

		tcp := &dns.Client{Net: "tcp", Timeout: r.timeout}
		resp, _, err := tcp.ExchangeContext(ctx, q, server)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// ParseAnswer 从应答中提取 IP（A/AAAA）与 CNAME 目标。
func ParseAnswer(msg *dns.Msg) (ips []string, cnames []string) {
	if msg == nil {
		return nil, nil
	}
	for _, ans := range msg.Answer {
		switch rr := ans.(type) {
		case *dns.A:
			ips = append(ips, rr.A.String())
		case *dns.AAAA:
			ips = append(ips, rr.AAAA.String())
		case *dns.CNAME:
			cnames = append(cnames, rr.Target)
		}
	}
	return ips, cnames
}

// IsNXDOMAIN 判断是否名字不存在。
func IsNXDOMAIN(msg *dns.Msg) bool {
	return msg != nil && msg.Rcode == dns.RcodeNameError
}
