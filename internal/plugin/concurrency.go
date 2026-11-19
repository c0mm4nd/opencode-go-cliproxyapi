package plugin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"opencode-go-cliproxyapi/internal/config"
)

// One limiter survives reconfiguration. GLM models and all client protocols
// share an account's slots; labels/auth filenames are deliberately not identity.
type accountLimiter struct {
	mu     sync.Mutex
	groups map[[32]byte]*accountSlots
}

type accountSlots struct {
	active  int
	waiting []*slotWaiter
}

type slotWaiter struct {
	ctx     context.Context
	limit   int
	ready   chan struct{}
	granted bool // protected by accountLimiter.mu
}

// A timeout is not proof that a host HTTP callback stopped. References keep
// the slot occupied until the callback AND any late-stream cleanup complete.
type accountPermit struct {
	refs  atomic.Int64
	owner *accountLimiter
	group [32]byte
}

type permitContextKey struct{}

func (p *accountPermit) retain() {
	if p != nil {
		p.refs.Add(1)
	}
}

func (p *accountPermit) release() {
	if p != nil && p.refs.Add(-1) == 0 {
		p.owner.mu.Lock()
		defer p.owner.mu.Unlock()
		g := p.owner.groups[p.group]
		g.active--
		p.owner.dispatch(p.group, g)
	}
}

// Caller holds mu. FIFO admission prevents new sessions starving queued work.
func (l *accountLimiter) dispatch(key [32]byte, g *accountSlots) {
	for len(g.waiting) > 0 {
		w := g.waiting[0]
		if w.ctx.Err() == nil && g.active >= w.limit {
			break
		}
		g.waiting = g.waiting[1:]
		if w.ctx.Err() == nil {
			g.active++
			w.granted = true
		}
		close(w.ready)
	}
	if g.active == 0 && len(g.waiting) == 0 && l.groups[key] == g {
		delete(l.groups, key)
	}
}

func (l *accountLimiter) acquire(ctx context.Context, group [32]byte, limit int) (*accountPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.groups == nil {
		l.groups = make(map[[32]byte]*accountSlots)
	}
	g := l.groups[group]
	if g == nil {
		g = &accountSlots{}
		l.groups[group] = g
	}
	w := &slotWaiter{ctx: ctx, limit: limit, ready: make(chan struct{})}
	g.waiting = append(g.waiting, w)
	l.dispatch(group, g)
	l.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-w.ready:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		if w.granted {
			g.active--
		} else {
			for i, queued := range g.waiting {
				if queued == w {
					g.waiting = append(g.waiting[:i], g.waiting[i+1:]...)
					break
				}
			}
		}
		l.dispatch(group, g)
		return nil, err
	}
	p := &accountPermit{owner: l, group: group}
	p.refs.Store(1)
	return p, nil
}

func concurrencyGroup(cfg config.Config, key string) [32]byte {
	identity := "key:" + key
	for _, entry := range cfg.APIKeys {
		if entry.Value == key && entry.ConcurrencyGroup != "" {
			identity = "group:" + entry.ConcurrencyGroup
			break
		}
	}
	return sha256.Sum256([]byte(identity))
}

func (m *Manager) acquireExecution(ctx context.Context, res *resolvedExecution) (context.Context, *accountPermit, error) {
	if !strings.HasPrefix(strings.ToLower(res.rec.UpstreamID), "glm-") || res.cfg.MaxInFlight == 0 {
		return ctx, nil, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, res.cfg.QueueTimeout)
	defer cancel()
	p, err := m.limiter.acquire(waitCtx, concurrencyGroup(res.cfg, res.key), res.cfg.MaxInFlight)
	if err != nil {
		return ctx, nil, fmt.Errorf("local account concurrency wait failed: %w", err)
	}
	return context.WithValue(ctx, permitContextKey{}, p), p, nil
}
