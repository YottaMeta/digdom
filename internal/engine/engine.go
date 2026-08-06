package engine

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"digdom/internal/brute"
	"digdom/internal/model"
)

// Scanner 一次爆破任务的编排器：
// 并发池解析、可选限速、多级递归 BFS、全局去重、通配符分级。
// 结果与进度分别经 Results() / Progress() 通道流出，由调用方消费。
type Scanner struct {
	cfg     model.Config
	results chan model.Result
	progress chan model.Progress

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	seen     map[string]struct{}
	maxDepth int

	hits    atomic.Int64
	wild    atomic.Int64
	unrev   atomic.Int64
	queried atomic.Int64
	limiter *rate.Limiter
}

// NewScanner 构造扫描器。
func NewScanner(cfg model.Config) *Scanner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scanner{
		cfg:      cfg,
		results:  make(chan model.Result, 4096),
		progress: make(chan model.Progress, 8),
		ctx:      ctx,
		cancel:   cancel,
		seen:     make(map[string]struct{}),
		maxDepth: cfg.MaxDepth,
	}
}

// Results 返回结果通道。
func (s *Scanner) Results() <-chan model.Result { return s.results }

// Config 返回扫描配置（只读）。
func (s *Scanner) Config() model.Config { return s.cfg }

// Progress 返回进度通道。
func (s *Scanner) Progress() <-chan model.Progress { return s.progress }

// Stop 取消扫描（幂等）。
func (s *Scanner) Stop() { s.cancel() }

// Run 阻塞执行扫描；结束后关闭结果通道，返回汇总。
func (s *Scanner) Run() model.Stats {
	defer close(s.results)
	defer close(s.progress)
	start := time.Now()

	root := model.NormalizeName(s.cfg.Root)
	if root == "" {
		return model.Stats{Error: "目标为空"}
	}

	timeout := s.cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	concurrency := s.cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 300
	}
	resolver := brute.NewResolver(s.cfg.Resolvers, timeout, 2)
	if s.cfg.RPS > 0 {
		s.limiter = rate.NewLimiter(rate.Limit(s.cfg.RPS), s.cfg.RPS)
	}

	// 作业池：work 通道承担并发限制，pending 统计所有未完成作业（含 base 协程与解析任务）。
	// 用互斥计数替代 WaitGroup：递归会在任务执行中新增作业，WaitGroup 存在
	// Add 晚于 Wait 返回导致 send on closed channel 的竞态（复现为启动扫描即崩溃）。
	work := make(chan func(), concurrency)
	var mu sync.Mutex
	pending := 0
	done := make(chan struct{})

	finish := func() {
		mu.Lock()
		pending--
		if pending == 0 {
			close(done)
		}
		mu.Unlock()
	}

	// 阻塞式提交：限制并发不超过 concurrency，响应取消。
	submit := func(f func()) {
		mu.Lock()
		pending++
		mu.Unlock()
		select {
		case work <- f:
		case <-s.ctx.Done():
			finish()
		}
	}

	for i := 0; i < concurrency; i++ {
		go func() {
			for f := range work {
				f()
				finish()
			}
		}()
	}

	// 进度心跳
	stopTick := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopTick:
				return
			case <-t.C:
				s.emitProgress(true)
			}
		}
	}()

	// 递归：把 base 作为独立作业提交（计入 pending）。
	var runBase func(string, int)
	runBase = func(base string, depth int) {
		mu.Lock()
		pending++
		mu.Unlock()
		go func() {
			defer finish()
			s.processBase(resolver, submit, runBase, base, depth)
		}()
	}

	runBase(root, 0)
	<-done
	close(work)
	close(stopTick)
	s.emitProgress(false)

	return model.Stats{
		Queried:    int(s.queried.Load()),
		Hits:       int(s.hits.Load()),
		Wildcards:  int(s.wild.Load()),
		Unreviewed: int(s.unrev.Load()),
		DurationMS: time.Since(start).Milliseconds(),
	}
}

// processBase 处理一个 base 层级：先探测该层通配符，再对字典逐词提交解析。
func (s *Scanner) processBase(resolver *brute.Resolver, submit func(func()), runBase func(string, int), base string, depth int) {
	if depth > s.maxDepth || s.ctx.Err() != nil {
		return
	}
	wild := brute.DetectWildcard(s.ctx, resolver, base)
	for _, word := range s.cfg.DictWords {
		if s.ctx.Err() != nil {
			return
		}
		name := model.NormalizeName(word + "." + base)
		if !s.trySeen(name) {
			continue
		}
		name, base, depth, wild := name, base, depth, wild
		submit(func() {
			s.resolveOne(resolver, name, base, depth, wild, runBase)
		})
	}
}

// resolveOne 单条解析 + 分类 + 可能的递归入队。
func (s *Scanner) resolveOne(resolver *brute.Resolver, name, base string, depth int, wild *brute.WildcardInfo, runBase func(string, int)) {
	if s.limiter != nil {
		if err := s.limiter.Wait(s.ctx); err != nil {
			return
		}
	}
	jitterSleep(s.ctx)

	s.queried.Add(1)
	msg, err := resolver.Resolve(s.ctx, name)
	if err != nil {
		if s.ctx.Err() != nil {
			return
		}
		s.pushResult(model.Result{Name: name, Base: base, Depth: depth, Tag: model.TagUnreviewed})
		s.unrev.Add(1)
		return
	}
	if brute.IsNXDOMAIN(msg) {
		return
	}

	ips, cnames := brute.ParseAnswer(msg)
	tag := model.TagUnreviewed
	if len(ips) > 0 {
		tag = model.TagHit
	}
	if wild != nil && wild.Detected && len(wild.IPs) > 0 && ipSetsEqual(ips, wild.IPs) {
		tag = model.TagWildcard
	}

	switch tag {
	case model.TagHit:
		s.hits.Add(1)
	case model.TagWildcard:
		s.wild.Add(1)
	default:
		s.unrev.Add(1)
	}

	// 递归：命中且有真实 IP、未超深度 → 入队下级（该名字在 seen 中唯一，天然只入队一次）
	if tag == model.TagHit && len(ips) > 0 && depth < s.maxDepth {
		runBase(name, depth+1)
	}

	s.pushResult(model.Result{
		Name:   name,
		IPs:    model.DedupeStrings(ips),
		CNAMEs: model.DedupeStrings(cnames),
		Tag:    tag,
		Base:   base,
		Depth:  depth,
	})
}

// jitterSleep 随机延时 0~30ms，规避权威 DNS 防爆破。
func jitterSleep(ctx context.Context) {
	d := time.Duration(rand.Intn(30)) * time.Millisecond
	if d == 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ipSetsEqual 比较两个 IP 集合等价（忽略顺序）。
func ipSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(b))
	for _, ip := range b {
		set[ip] = struct{}{}
	}
	for _, ip := range a {
		if _, ok := set[ip]; !ok {
			return false
		}
	}
	return true
}

// pushResult 发送结果。阻塞等待直到通道腾出或被取消（根治通道满静默丢结果；
// 消费方始终在读取，不会死锁）。
func (s *Scanner) pushResult(r model.Result) {
	if r.IPs == nil {
		r.IPs = []string{}
	}
	if r.CNAMEs == nil {
		r.CNAMEs = []string{}
	}
	select {
	case s.results <- r:
	case <-s.ctx.Done():
	}
}

// emitProgress 发送进度帧（非阻塞）。
func (s *Scanner) emitProgress(active bool) {
	p := model.Progress{
		Queried:    int(s.queried.Load()),
		Hits:       int(s.hits.Load()),
		Wildcards:  int(s.wild.Load()),
		Unreviewed: int(s.unrev.Load()),
		Active:     active,
	}
	select {
	case s.progress <- p:
	default:
	}
}

// trySeen 去重，返回 false 表示已见过。
func (s *Scanner) trySeen(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}
