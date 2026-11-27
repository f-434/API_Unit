package api

import (
	"sync"
	"time"
)

const (
	loginMaxFailures   = 5
	loginFailureWindow = 10 * time.Minute
	loginBlockDuration = 15 * time.Minute
	loginLimiterMax    = 4096
)

// loginGuard 对登录失败做按来源 IP 的滑动窗口限制，防止公网暴力破解。
type loginGuard struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

type loginEntry struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
	lastSeen     time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{entries: map[string]*loginEntry{}}
}

// blocked 返回该来源是否仍在封禁期。
func (g *loginGuard) blocked(key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil {
		return false, 0
	}
	now := time.Now()
	if now.Before(e.blockedUntil) {
		return true, time.Until(e.blockedUntil)
	}
	return false, 0
}

// fail 记录一次密码错误；连续失败达到阈值后封禁一段时间。
func (g *loginGuard) fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	e := g.entries[key]
	if e == nil {
		if len(g.entries) >= loginLimiterMax {
			g.pruneLocked(now)
		}
		e = &loginEntry{windowStart: now}
		g.entries[key] = e
	}
	if now.Sub(e.windowStart) > loginFailureWindow {
		e.failures = 0
		e.windowStart = now
	}
	e.failures++
	e.lastSeen = now
	if e.failures >= loginMaxFailures {
		e.blockedUntil = now.Add(loginBlockDuration)
		e.failures = 0
		e.windowStart = now
	}
}

// reset 在登录成功后清除失败记录。
func (g *loginGuard) reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
}

// pruneLocked 清理已过期条目，避免长期运行后 map 无限增长。
func (g *loginGuard) pruneLocked(now time.Time) {
	cutoff := now.Add(-loginFailureWindow - loginBlockDuration)
	for k, e := range g.entries {
		if e.lastSeen.Before(cutoff) && now.After(e.blockedUntil) {
			delete(g.entries, k)
		}
	}
}
