// Package httpcheck 用 Go 内置 net/http 对域名做存活探测（HTTP/HTTPS），
// 用于批量复核：可达即判为有效，替代逐个人工点击。不依赖外部 httpx 二进制。
package httpcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Result 单个域名的探测结果。
type Result struct {
	Scheme string // https / http
	Status int    // HTTP 状态码，0 表示失败/不可达
	Note   string // 人类可读备注
	OK     bool   // 是否可达
}

// Check 对 host 先试 HTTPS 再试 HTTP，返回探测结果。
func Check(ctx context.Context, host string, timeout time.Duration) Result {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	var last string
	for _, scheme := range []string{"https", "http"} {
		if ctx.Err() != nil {
			return Result{OK: false, Note: "已取消"}
		}
		url := scheme + "://" + host
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "DigDom/0.2")
		resp, err := client.Do(req)
		if err != nil {
			last = err.Error()
			continue
		}
		// 只消费少量字节便于连接复用，避免整页下载。
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		_ = resp.Body.Close()
		return Result{Scheme: scheme, Status: resp.StatusCode, OK: true,
			Note: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	if last == "" {
		last = "无可用协议"
	}
	return Result{OK: false, Note: "不可达: " + last}
}

// CheckAll 并发探活一批 host，结果与输入顺序一致。concurrency 限制在途请求数。
func CheckAll(ctx context.Context, hosts []string, timeout time.Duration, concurrency int) []Result {
	if concurrency <= 0 {
		concurrency = 50
	}
	out := make([]Result, len(hosts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i := range hosts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				out[i] = Result{OK: false, Note: "已取消"}
				return
			}
			out[i] = Check(ctx, hosts[i], timeout)
		}(i)
	}
	wg.Wait()
	return out
}