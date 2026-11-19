package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"opencode-go-cliproxyapi/internal/config"
)

func waitCondition(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLimiterFIFOAndCancellation(t *testing.T) {
	var l accountLimiter
	key := sha256.Sum256([]byte("account"))
	p, err := l.acquire(context.Background(), key, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := l.acquire(ctx, key, 1); canceled <- err }()
	waitCondition(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.groups[key].waiting) == 1 })
	second := make(chan *accountPermit, 1)
	go func() { p, _ := l.acquire(context.Background(), key, 1); second <- p }()
	waitCondition(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.groups[key].waiting) == 2 })
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	p.release()
	select {
	case p := <-second:
		p.release()
	case <-time.After(time.Second):
		t.Fatal("canceled waiter stranded next request")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.groups) != 0 {
		t.Fatal("idle group leaked")
	}
}

func TestLimiterCanceledAdmissionRace(t *testing.T) {
	// Exercise cancellation racing with slot handoff and idle group replacement.
	var l accountLimiter
	key := sha256.Sum256([]byte("account"))
	for i := 0; i < 200; i++ {
		p, _ := l.acquire(context.Background(), key, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { q, _ := l.acquire(ctx, key, 1); q.release(); close(done) }()
		cancel()
		p.release()
		r, _ := l.acquire(context.Background(), key, 1)
		r.release()
		<-done
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.groups) != 0 {
		t.Fatal("canceled admission leaked a slot")
	}
}

func TestConcurrencyGroups(t *testing.T) {
	c := config.Config{APIKeys: []config.APIKey{{Value: "a", ConcurrencyGroup: "workspace"}, {Value: "b", ConcurrencyGroup: "workspace"}}}
	if concurrencyGroup(c, "a") != concurrencyGroup(c, "b") {
		t.Fatal("workspace keys must share slots")
	}
	if concurrencyGroup(c, "a") == concurrencyGroup(c, "unknown") {
		t.Fatal("unknown selected key must have its own slots")
	}
	if concurrencyGroup(config.Config{}, "a") == concurrencyGroup(config.Config{}, "b") {
		t.Fatal("independent keys were grouped")
	}
}

func TestAccountLimiterSurvivesReconfigureAndGroupsModels(t *testing.T) {
	m, f := newTestManager(catalogResponder(true, `{"data":[{"id":"glm-5.3"},{"id":"glm-5.3-flash"}]}`))
	t.Cleanup(func() { m.HandleCall("plugin.shutdown", nil) })
	_ = f
	doc := "api-keys:\n  - value: a\n    concurrency-group: workspace\n  - value: b\n    concurrency-group: workspace\nmax-in-flight: 1\nqueue-timeout: 10ms\n"
	m.HandleCall("plugin.register", lifecycleRequestBody(doc))
	r := &resolvedExecution{cfg: m.cfg, key: "a"}
	r.rec.UpstreamID = "glm-5.3"
	_, p, err := m.acquireExecution(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer p.release()
	m.HandleCall("plugin.reconfigure", lifecycleRequestBody(doc))
	r = &resolvedExecution{cfg: m.cfg, key: "b"}
	r.rec.UpstreamID = "GLM-5.3-flash"
	if _, q, err := m.acquireExecution(context.Background(), r); !errors.Is(err, context.DeadlineExceeded) {
		q.release()
		t.Fatalf("reconfigure/group/model bypassed held slot: %v", err)
	}
}

func TestAccountNonStreamConcurrencyAndOtherAccounts(t *testing.T) {
	entered := make(chan string, 32)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := &fakeCaller{}
	m := NewManager(NewHostBridge(f.call))
	t.Cleanup(func() { once.Do(func() { close(release) }); m.HandleCall("plugin.shutdown", nil) })
	f.responder = wrapWithCatalog(multiRouteCatalog, func(method string, payload []byte) ([]byte, error) {
		if method != pluginabi.MethodHostHTTPDo {
			return hostOK(struct{}{}), nil
		}
		var r hostHTTPReq
		json.Unmarshal(payload, &r)
		entered <- r.Headers.Get("Authorization")
		<-release
		body := ccResponseBody
		if strings.HasSuffix(r.URL, "/messages") {
			body = claudeResponseBody
		}
		if strings.HasSuffix(r.URL, "/responses") {
			body = responsesPassthrough
		}
		return hostOK(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(body)}), nil
	})
	m.HandleCall("plugin.register", lifecycleRequestBody(testValidYAML))
	results := make(chan []byte, 10)
	for i := 0; i < 7; i++ {
		model := "glm-5.3"
		go func(model string) {
			r, _ := m.handleExecute(execReqBody("opencode-go/"+model, "openai", []byte(ccRequestBody), false))
			results <- r
		}(model)
	}
	for i := 0; i < 6; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("six slots not admitted")
		}
	}
	select {
	case <-entered:
		t.Fatal("seventh request reached upstream")
	case <-time.After(25 * time.Millisecond):
	}
	// Other families bypass this gate even when the same account's GLM slots are full.
	for _, model := range []string{"minimax-m3", "gpt-5.6-luna"} {
		go func(model string) {
			r, _ := m.handleExecute(execReqBody("opencode-go/"+model, "openai", []byte(ccRequestBody), false))
			results <- r
		}(model)
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("saturated GLM slots blocked %s", model)
		}
	}
	// CPA can select a credential not present in materialization config.
	var req executorRequest
	json.Unmarshal(execReqBody("opencode-go/glm-5.3", "openai", []byte(ccRequestBody), false), &req)
	req.AuthAttributes["api_key"] = "other-account"
	raw, _ := json.Marshal(req)
	go func() { r, _ := m.handleExecute(raw); results <- r }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("one saturated account blocked another")
	}
	once.Do(func() { close(release) })
	for i := 0; i < 10; i++ {
		select {
		case r := <-results:
			if !decodeEnv(t, r).OK {
				t.Fatalf("execution failed: %s", r)
			}
		case <-time.After(time.Second):
			t.Fatal("queued execution did not complete")
		}
	}
}

func TestAccountStreamsHoldSlotsUntilClose(t *testing.T) {
	read := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(read) })
	var starts atomic.Int32
	f := &fakeCaller{}
	m := NewManager(NewHostBridge(f.call))
	t.Cleanup(func() { release.Do(func() { close(read) }); m.HandleCall("plugin.shutdown", nil) })
	f.responder = wrapWithCatalog(multiRouteCatalog, func(method string, payload []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodHostHTTPDoStream:
			n := starts.Add(1)
			return hostOK(hostStreamStartResp{StatusCode: 200, StreamID: fmt.Sprintf("up-%d", n)}), nil
		case pluginabi.MethodHostHTTPStreamRead:
			<-read
			return hostOK(hostStreamReadResp{Payload: []byte("data: [DONE]\n\n"), Done: true}), nil
		case pluginabi.MethodHostHTTPDo:
			starts.Add(1)
			return hostOK(pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(ccResponseBody)}), nil
		}
		return hostOK(struct{}{}), nil
	})
	m.HandleCall("plugin.register", lifecycleRequestBody(testValidYAML))
	for i := 0; i < 6; i++ {
		r, _ := m.handleExecuteStream(execStreamReqBody("opencode-go/glm-5.3", "openai", []byte(ccRequestBody), fmt.Sprintf("down-%d", i)))
		if !decodeEnv(t, r).OK {
			t.Fatalf("start failed: %s", r)
		}
	}
	done := make(chan []byte, 1)
	go func() {
		r, _ := m.handleExecute(execReqBody("opencode-go/glm-5.3", "openai", []byte(ccRequestBody), false))
		done <- r
	}()
	time.Sleep(25 * time.Millisecond)
	if starts.Load() != 6 {
		t.Fatal("stream returned its slot before closing")
	}
	release.Do(func() { close(read) })
	select {
	case r := <-done:
		if !decodeEnv(t, r).OK {
			t.Fatalf("queued execution failed: %s", r)
		}
	case <-time.After(time.Second):
		t.Fatal("stream cleanup did not release slot")
	}
}

func TestPermitSurvivesTimedOutHostCallback(t *testing.T) {
	var l accountLimiter
	key := sha256.Sum256([]byte("account"))
	p, _ := l.acquire(context.Background(), key, 1)
	hostEntered := make(chan struct{})
	hostExit := make(chan struct{})
	b := NewHostBridge(func(string, []byte) ([]byte, error) { close(hostEntered); <-hostExit; return hostOK(struct{}{}), nil })
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), permitContextKey{}, p), 20*time.Millisecond)
	defer cancel()
	_, err := b.Do(ctx, pluginapi.HTTPRequest{})
	if err == nil {
		t.Fatal("expected callback timeout")
	}
	<-hostEntered
	p.release()
	wait, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if q, err := l.acquire(wait, key, 1); err == nil {
		q.release()
		t.Fatal("timed out callback prematurely released slot")
	}
	close(hostExit)
	if !b.WaitForInFlight(time.Second) {
		t.Fatal("callback did not drain")
	}
	q, err := l.acquire(context.Background(), key, 1)
	if err != nil {
		t.Fatal(err)
	}
	q.release()
}

func TestQueueTimeoutAndShutdown(t *testing.T) {
	m := NewManager(nil)
	cfg := config.Config{MaxInFlight: 1, QueueTimeout: 10 * time.Millisecond}
	res := &resolvedExecution{cfg: cfg, key: "a"}
	res.rec.UpstreamID = "glm-5.3"
	_, p, err := m.acquireExecution(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.acquireExecution(context.Background(), res)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue timeout = %v", err)
	}
	res.cfg.QueueTimeout = time.Minute
	done := make(chan error, 1)
	go func() { _, q, err := m.acquireExecution(m.executionContext, res); q.release(); done <- err }()
	m.handleShutdown()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown wait = %v", err)
	}
	p.release()
	res.rec.UpstreamID = "minimax-m3"
	_, q, err := m.acquireExecution(context.Background(), res)
	if err != nil || q != nil {
		t.Fatal("non-GLM request was limited")
	}
	q.release()
	res.rec.UpstreamID = "glm-5.3"
	res.cfg.MaxInFlight = 0
	if _, q, err := m.acquireExecution(context.Background(), res); err != nil || q != nil {
		t.Fatal("disabled account limit still applied")
	}
}

func TestPermitSurvivesLateStreamAndCleanup(t *testing.T) {
	oldGrace := streamTimeoutGrace
	streamTimeoutGrace = time.Millisecond
	defer func() { streamTimeoutGrace = oldGrace }()
	var l accountLimiter
	key := sha256.Sum256([]byte("account"))
	p, _ := l.acquire(context.Background(), key, 1)
	finishStart := make(chan struct{})
	closeEntered := make(chan struct{})
	finishClose := make(chan struct{})
	b := NewHostBridge(func(method string, _ []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodHostHTTPDoStream:
			<-finishStart
			return hostOK(hostStreamStartResp{StatusCode: 200, StreamID: "late"}), nil
		case pluginabi.MethodHostHTTPStreamClose:
			close(closeEntered)
			<-finishClose
		}
		return hostOK(struct{}{}), nil
	})
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), permitContextKey{}, p), 10*time.Millisecond)
	defer cancel()
	_, _, _, err := b.DoStream(ctx, pluginapi.HTTPRequest{})
	if err == nil {
		t.Fatal("expected stream start timeout")
	}
	p.release()
	close(finishStart)
	<-closeEntered
	wait, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if q, err := l.acquire(wait, key, 1); err == nil {
		q.release()
		t.Fatal("late stream cleanup released slot before close completed")
	}
	close(finishClose)
	if !b.WaitForInFlight(time.Second) {
		t.Fatal("late cleanup did not drain")
	}
	q, err := l.acquire(context.Background(), key, 1)
	if err != nil {
		t.Fatal(err)
	}
	q.release()
}

func TestRejectedStreamTimeoutCleanupRetainsPermit(t *testing.T) {
	var l accountLimiter
	key := sha256.Sum256([]byte("account"))
	p, _ := l.acquire(context.Background(), key, 1)
	closeEntered := make(chan struct{})
	finishClose := make(chan struct{})
	var closes atomic.Int32
	b := NewHostBridge(func(method string, _ []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodHostHTTPStreamRead:
			<-closeEntered
			return hostOK(hostStreamReadResp{Done: true}), nil
		case pluginabi.MethodHostHTTPStreamClose:
			if closes.Add(1) == 1 {
				close(closeEntered)
				<-finishClose
			}
		}
		return hostOK(struct{}{}), nil
	})
	m := NewManager(b)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), permitContextKey{}, p), 10*time.Millisecond)
	defer cancel()
	m.readStreamError(ctx, "rejected", 1024)
	p.release()
	wait, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if q, err := l.acquire(wait, key, 1); err == nil {
		q.release()
		t.Fatal("rejected stream timeout cleanup released slot early")
	}
	close(finishClose)
	if !b.WaitForInFlight(time.Second) {
		t.Fatal("rejected stream cleanup did not drain")
	}
	q, err := l.acquire(context.Background(), key, 1)
	if err != nil {
		t.Fatal(err)
	}
	q.release()
	m.handleShutdown()
}
