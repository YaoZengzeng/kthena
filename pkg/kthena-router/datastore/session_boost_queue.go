/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package datastore

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/kthena/pkg/kthena-router/metrics"
)

// BackendWaitingChecker is a function that checks whether the backend pods
// have capacity to accept new requests. It returns true when at least one pod
// has a waiting queue length within the configured tolerance (i.e.
// RequestWaitingNum <= BackendWaitingTolerance), meaning the backend can accept
// a new request without excessive queuing.
type BackendWaitingChecker func() bool

// SessionTracker tracks recently completed sessions for priority boosting.
// It maps correlation IDs to their last completion time, allowing follow-up
// requests in the same session to be prioritized for prefix cache utilization.
type SessionTracker struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // sessionID -> last completion time
	ttl      time.Duration
}

// NewSessionTracker creates a new session tracker with the given TTL.
func NewSessionTracker(ttl time.Duration) *SessionTracker {
	return &SessionTracker{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
}

// MarkCompleted records that a request from the given session has completed.
func (st *SessionTracker) MarkCompleted(sessionID string) {
	if sessionID == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[sessionID] = time.Now()
}

// HasRecentCompletion checks if the given session ID has a completion within the TTL window.
func (st *SessionTracker) HasRecentCompletion(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	completionTime, exists := st.sessions[sessionID]
	if !exists {
		return false
	}
	return time.Since(completionTime) <= st.ttl
}

// Cleanup removes expired sessions. Should be called periodically.
func (st *SessionTracker) Cleanup() {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	expired := 0
	for id, t := range st.sessions {
		if now.Sub(t) > st.ttl {
			delete(st.sessions, id)
			expired++
		}
	}
	if expired > 0 {
		klog.V(4).Infof("[SessionTracker] cleanup: removed %d expired sessions, remaining=%d", expired, len(st.sessions))
	}
}

// ActiveSessions returns the number of sessions currently tracked.
func (st *SessionTracker) ActiveSessions() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.sessions)
}

// SessionBoostQueueConfig holds configurable parameters for the standalone session boost queue.
type SessionBoostQueueConfig struct {
	// SessionIDHeader is the HTTP header name used to identify conversation sessions.
	// Configured via the SESSION_BOOST_HEADER environment variable. If not set,
	// session identification is disabled.
	SessionIDHeader string

	// SessionBoostTTL is the duration after which a session boost expires.
	// Requests from the same session that arrive within this window after the
	// previous request completed will be boosted.
	SessionBoostTTL time.Duration

	// SessionBoostGracePeriod is the duration to wait after a release before dequeuing
	// the next request in backpressure mode.
	// This gives the same session time to submit a follow-up request that benefits
	// from prefix cache, rather than immediately dispatching an unrelated request.
	// If a session-boosted request arrives during this window, it is dequeued immediately.
	// Defaults to 50ms. Set to 0 to disable the grace period.
	SessionBoostGracePeriod time.Duration

	// BackpressurePollInterval controls how often the backpressure checker polls
	// backend pod waiting queue status. Defaults to 100ms.
	BackpressurePollInterval time.Duration

	// InflightPerPod is the maximum number of inflight requests allowed per backend pod.
	// The total inflight limit is InflightPerPod * podCount.
	// Defaults to 1.
	InflightPerPod int

	// EnableWaitPromotion controls whether the wait-based promotion feature is enabled.
	// When enabled, non-boosted requests that wait longer than MaxWaitBeforePromotion
	// are promoted to boosted priority to prevent starvation.
	// Configured via SESSION_BOOST_WAIT_PROMOTION_ENABLED env var. Defaults to true.
	EnableWaitPromotion bool

	// MaxWaitBeforePromotion is the maximum time a non-boosted request can wait
	// before being promoted to boosted priority. This prevents starvation of
	// first-turn requests under high multi-turn load.
	// Only effective when EnableWaitPromotion is true.
	// Configured via SESSION_BOOST_MAX_WAIT env var. Defaults to 5s.
	//
	// When WaitTimeoutReject is enabled, this same threshold determines how long a
	// non-boosted request may wait before it is rejected with HTTP 429.
	MaxWaitBeforePromotion time.Duration

	// WaitTimeoutReject selects the rejection mode for the wait-timeout behavior.
	// When true, a non-boosted request that waits at least MaxWaitBeforePromotion
	// is removed from the queue and the caller returns HTTP 429 (Too Many Requests)
	// instead of being promoted to boosted priority. This provides load shedding
	// under sustained overload. It is mutually exclusive with wait promotion: when
	// enabled, aging promotion is disabled and MaxWaitBeforePromotion is reused as
	// the reject threshold.
	// Configured via SESSION_BOOST_WAIT_REJECT_ENABLED env var. Defaults to false.
	WaitTimeoutReject bool

	// BackendWaitingTolerance is the maximum number of waiting requests allowed
	// on a backend pod while still considering it as having capacity.
	// For example, if set to 1, a pod with RequestWaitingNum <= 1 is considered
	// to have capacity. If set to 0 (default), only pods with no waiting requests
	// are considered to have capacity.
	// Configured via SESSION_BOOST_BACKEND_WAITING_TOLERANCE env var. Defaults to 0.
	BackendWaitingTolerance int

	// MetricsScrapeInterval is the interval at which backend pod metrics are scraped.
	// Used by the burst limiter to determine how long dispatched requests take to
	// be reflected in backend metrics. Dispatches within one scrape window are
	// counted against the burst limit to prevent over-dispatching due to stale metrics.
	// Should match the METRICS_SCRAPE_INTERVAL env var. Defaults to 1s.
	MetricsScrapeInterval time.Duration
}

// DefaultSessionBoostQueueConfig returns default configuration for the session boost queue.
func DefaultSessionBoostQueueConfig() SessionBoostQueueConfig {
	return SessionBoostQueueConfig{
		SessionIDHeader:          "", // Must be set via SESSION_BOOST_HEADER env var
		SessionBoostTTL:          60 * time.Second,
		SessionBoostGracePeriod:  0,
		BackpressurePollInterval: 100 * time.Millisecond,
		InflightPerPod:           16,
		EnableWaitPromotion:      true,
		MaxWaitBeforePromotion:   5 * time.Second,
		WaitTimeoutReject:        false,
		BackendWaitingTolerance:  0,
		MetricsScrapeInterval:    1 * time.Second,
	}
}

// sessionBoostHeap implements heap.Interface for session boost priority ordering.
// Boosted requests always take priority over non-boosted ones, unless a non-boosted
// request has waited longer than maxWait (aging promotion to prevent starvation).
// Within the same effective boost status, FIFO ordering is used.
type sessionBoostHeap struct {
	items   []*Request
	maxWait time.Duration // MaxWaitBeforePromotion; 0 disables aging
}

func (h *sessionBoostHeap) Len() int { return len(h.items) }

func (h *sessionBoostHeap) Less(i, j int) bool {
	bi := h.effectiveBoosted(h.items[i])
	bj := h.effectiveBoosted(h.items[j])
	if bi != bj {
		return bi
	}
	// Within same effective boost status, use FIFO ordering.
	return h.items[i].RequestTime.Before(h.items[j].RequestTime)
}

// effectiveBoosted returns true if the request is session-boosted OR has waited
// longer than the configured maxWait threshold (aging promotion).
func (h *sessionBoostHeap) effectiveBoosted(r *Request) bool {
	if r.SessionBoost {
		return true
	}
	if h.maxWait > 0 && time.Since(r.RequestTime) >= h.maxWait {
		return true
	}
	return false
}

func (h *sessionBoostHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *sessionBoostHeap) Push(x interface{}) {
	h.items = append(h.items, x.(*Request))
}

func (h *sessionBoostHeap) Pop() interface{} {
	n := len(h.items)
	if n == 0 {
		return nil
	}
	item := h.items[n-1]
	h.items[n-1] = nil
	h.items = h.items[0 : n-1]
	return item
}

// SessionBoostQueue implements session-aware priority boosting for multi-turn
// conversations to maximize prefix cache hit rate on LLM inference backends.
type SessionBoostQueue struct {
	stopCh   chan struct{}
	notifyCh chan struct{}
	mu       sync.RWMutex
	heap     sessionBoostHeap
	metrics  *metrics.Metrics
	config   SessionBoostQueueConfig

	// Session tracking for priority boosting
	sessionTracker *SessionTracker

	// Backend capacity checking
	backendChecker BackendWaitingChecker

	// Inflight tracking for backpressure mode
	inflightCount atomic.Int64
	releaseCh     chan struct{}
	podCounter    func() int

	// Rate-limiting for backpressure logs to avoid excessive noise
	lastBackpressureLog atomic.Int64 // unix nano timestamp of last backpressure log

	// Burst tracking across passes: dispatches within one metrics scrape window
	// are counted to prevent over-dispatching due to stale backend metrics.
	burstDispatched  atomic.Int64 // dispatches since last burst window reset
	burstWindowStart atomic.Int64 // unix nano of current burst window start
}

// NewSessionBoostQueue creates a new standalone session boost queue.
func NewSessionBoostQueue(metricsInstance *metrics.Metrics, cfg SessionBoostQueueConfig, checker ...BackendWaitingChecker) *SessionBoostQueue {
	if metricsInstance == nil {
		metricsInstance = metrics.DefaultMetrics
	}
	maxWait := cfg.MaxWaitBeforePromotion
	if !cfg.EnableWaitPromotion || cfg.WaitTimeoutReject {
		// In reject mode, long-waiting requests are shed rather than promoted, so
		// the heap must not reorder them as boosted. Disable aging promotion.
		maxWait = 0
	}
	q := &SessionBoostQueue{
		stopCh:         make(chan struct{}),
		notifyCh:       make(chan struct{}, 1),
		releaseCh:      make(chan struct{}, 1),
		heap:           sessionBoostHeap{items: make([]*Request, 0), maxWait: maxWait},
		metrics:        metricsInstance,
		config:         cfg,
		sessionTracker: NewSessionTracker(cfg.SessionBoostTTL),
	}
	if len(checker) > 0 && checker[0] != nil {
		q.backendChecker = checker[0]
	}
	return q
}

// PushRequest adds a request to the session boost queue.
// If the request's session ID matches a recently completed session, it is boosted.
func (q *SessionBoostQueue) PushRequest(r *Request) error {
	q.mu.Lock()

	// Check session boost: if this request's session ID has a recent completion,
	// mark it as boosted so it gets priority in the queue.
	if r.SessionID != "" && q.sessionTracker.HasRecentCompletion(r.SessionID) {
		r.SessionBoost = true
	}

	heap.Push(&q.heap, r)
	queueLen := q.heap.Len()
	q.mu.Unlock()

	// Update metrics
	if q.metrics != nil {
		q.metrics.IncSessionBoostQueueSize(r.ModelName)
	}

	if r.SessionBoost {
		klog.V(2).Infof("[SessionBoostQueue] *** BOOSTED *** reqID=%s sessionID=%s reason=session_cache_hit queueLen=%d",
			r.ReqID, r.SessionID, queueLen)
	}

	// Signal that a new item is available
	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
	return nil
}

// popWhenAvailable blocks until an item is available or the context is done.
func (q *SessionBoostQueue) popWhenAvailable(ctx context.Context) (*Request, error) {
	for {
		q.mu.Lock()
		if q.heap.Len() > 0 {
			req := heap.Pop(&q.heap).(*Request)

			// Skip cancelled requests
			if req.isCancelled() {
				if q.metrics != nil {
					q.metrics.DecSessionBoostQueueSize(req.ModelName)
					queueDuration := time.Since(req.RequestTime)
					q.metrics.RecordSessionBoostQueueDuration(req.ModelName, queueDuration)
					q.metrics.IncSessionBoostQueueCancelled(req.ModelName)
				}
				q.mu.Unlock()
				continue
			}

			queueDuration := time.Since(req.RequestTime)
			if q.metrics != nil {
				q.metrics.DecSessionBoostQueueSize(req.ModelName)
				q.metrics.RecordSessionBoostQueueDuration(req.ModelName, queueDuration)
				q.metrics.IncSessionBoostQueueDequeue(req.ModelName)
			}
			q.mu.Unlock()
			return req, nil
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.stopCh:
			return nil, errors.New("queue stopped")
		case <-q.notifyCh:
			continue
		}
	}
}

// Run starts the dequeue loop. Uses backpressure mode when a backend checker is provided,
// otherwise falls through immediately (no rate limiting — suitable for direct dispatch).
func (q *SessionBoostQueue) Run(ctx context.Context) {
	// Start session tracker cleanup goroutine
	go q.runSessionCleanup(ctx)

	// Start the wait-timeout reject sweeper when reject mode is enabled. It sheds
	// requests that have waited past MaxWaitBeforePromotion with HTTP 429,
	// independent of which dequeue mode is in use.
	if q.config.WaitTimeoutReject && q.config.MaxWaitBeforePromotion > 0 {
		go q.runRejectSweeper(ctx)
	}

	if q.backendChecker != nil {
		q.runBackpressureMode(ctx)
		return
	}
	// Without backpressure checker, dequeue immediately (no rate limiting).
	q.runDirectMode(ctx)
}

// runRejectSweeper periodically rejects non-boosted requests that have waited
// longer than MaxWaitBeforePromotion. Only started when WaitTimeoutReject is
// enabled. The sweep interval follows BackpressurePollInterval so rejection
// reacts promptly to the configured wait threshold.
func (q *SessionBoostQueue) runRejectSweeper(ctx context.Context) {
	interval := q.config.BackpressurePollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	klog.V(4).Infof("[SessionBoostQueue] starting wait-timeout reject sweeper, interval=%v, maxWait=%v",
		interval, q.config.MaxWaitBeforePromotion)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.rejectAgedRequests()
		}
	}
}

// rejectAgedRequests removes non-boosted requests that have waited at least
// MaxWaitBeforePromotion and marks them rejected so the caller returns HTTP 429.
// Boosted requests are never rejected here. This is the load-shedding counterpart
// of aging promotion: rather than promoting a long-waiting request, the queue
// sheds it. No-op unless WaitTimeoutReject is enabled.
func (q *SessionBoostQueue) rejectAgedRequests() {
	if !q.config.WaitTimeoutReject || q.config.MaxWaitBeforePromotion <= 0 {
		return
	}
	maxWait := q.config.MaxWaitBeforePromotion

	q.mu.Lock()
	if q.heap.Len() == 0 {
		q.mu.Unlock()
		return
	}
	survivors := make([]*Request, 0, q.heap.Len())
	var rejected []*Request
	for _, it := range q.heap.items {
		if it == nil {
			continue
		}
		if !it.SessionBoost && time.Since(it.RequestTime) >= maxWait {
			rejected = append(rejected, it)
		} else {
			survivors = append(survivors, it)
		}
	}
	if len(rejected) == 0 {
		q.mu.Unlock()
		return
	}
	q.heap.items = survivors
	heap.Init(&q.heap)
	q.mu.Unlock()

	// Notify rejected callers outside the lock. The requests are no longer in the
	// heap, so no dequeue path can observe them, making the channel close safe.
	for _, req := range rejected {
		req.Rejected = true
		if q.metrics != nil {
			q.metrics.DecSessionBoostQueueSize(req.ModelName)
			q.metrics.RecordSessionBoostQueueDuration(req.ModelName, time.Since(req.RequestTime))
			q.metrics.IncSessionBoostQueueRejected(req.ModelName)
		}
		klog.V(2).Infof("[SessionBoostQueue] *** REJECTED (429) *** reqID=%s user=%s model=%s sessionID=%s reason=wait_timeout waited=%v maxWait=%v",
			req.ReqID, req.UserID, req.ModelName, req.SessionID,
			time.Since(req.RequestTime).Round(time.Millisecond), maxWait)
		if req.NotifyChan != nil {
			close(req.NotifyChan)
		}
	}
}

// runDirectMode dequeues requests as fast as they arrive with no rate limiting.
func (q *SessionBoostQueue) runDirectMode(ctx context.Context) {
	for {
		req, err := q.popWhenAvailable(ctx)
		if err != nil {
			return
		}
		if req == nil || req.NotifyChan == nil {
			continue
		}
		if req.isCancelled() {
			continue
		}

		// Track inflight
		q.inflightCount.Add(1)
		releaseOnce := sync.Once{}
		req.Release = func() {
			releaseOnce.Do(func() {
				q.inflightCount.Add(-1)
				select {
				case q.releaseCh <- struct{}{}:
				default:
				}
				if q.metrics != nil {
					q.metrics.DecSessionBoostQueueInflight(req.ModelName)
				}
			})
		}
		if q.metrics != nil {
			q.metrics.IncSessionBoostQueueInflight(req.ModelName)
		}
		close(req.NotifyChan)
	}
}

// runSessionCleanup periodically cleans up expired sessions from the session tracker.
func (q *SessionBoostQueue) runSessionCleanup(ctx context.Context) {
	cleanupInterval := q.config.SessionBoostTTL
	if cleanupInterval < 10*time.Second {
		cleanupInterval = 10 * time.Second
	}
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.sessionTracker.Cleanup()
		}
	}
}

// runBackpressureMode dequeues requests only when backend pods have capacity.
// Uses two-level admission control:
//  1. Inflight limit: at most InflightPerPod requests per backend pod.
//  2. Backend metrics check: at least one pod reports capacity available.
//
// Session Grace Period: When SessionBoostGracePeriod > 0, a release event triggers
// a short wait before dequeuing to give the same session time to submit a follow-up
// request that can leverage prefix cache.
func (q *SessionBoostQueue) runBackpressureMode(ctx context.Context) {
	pollInterval := q.config.BackpressurePollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	klog.V(4).Infof("[SessionBoostQueue] starting backpressure dequeue loop, poll_interval=%v, gracePeriod=%v",
		pollInterval, q.config.SessionBoostGracePeriod)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	if q.config.SessionBoostGracePeriod > 0 {
		q.runBackpressureWithGrace(ctx, ticker)
	} else {
		q.runBackpressureNoGrace(ctx, ticker)
	}
}

// runBackpressureNoGrace is the fast path when grace period is disabled (default).
// Listens on notifyCh for immediate dequeue of freshly enqueued requests.
func (q *SessionBoostQueue) runBackpressureNoGrace(ctx context.Context, ticker *time.Ticker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-q.releaseCh:
			q.tryBackpressureDequeue(ctx)
		case <-q.notifyCh:
			q.tryBackpressureDequeue(ctx)
		case <-ticker.C:
			q.tryBackpressureDequeue(ctx)
		}
	}
}

// runBackpressureWithGrace handles the case where grace period is configured.
// Does NOT listen on notifyCh in the main select to avoid racing with the grace
// period logic on releaseCh. The ticker serves as the backstop for new arrivals.
func (q *SessionBoostQueue) runBackpressureWithGrace(ctx context.Context, ticker *time.Ticker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-q.releaseCh:
			q.waitGraceAndDequeue(ctx)
		case <-ticker.C:
			q.tryBackpressureDequeue(ctx)
		}
	}
}

// isHeadSessionBoosted checks if the highest-priority request in the queue has a session boost.
func (q *SessionBoostQueue) isHeadSessionBoosted() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.heap.Len() == 0 {
		return false
	}
	return q.heap.items[0].SessionBoost
}

// waitGraceAndDequeue waits up to SessionBoostGracePeriod for a session-boosted
// request to arrive at the head of the queue.
func (q *SessionBoostQueue) waitGraceAndDequeue(ctx context.Context) {
	// Fast path: head is already session-boosted.
	if q.isHeadSessionBoosted() {
		klog.V(2).Info("[SessionBoostQueue] *** BOOSTED *** grace: head already boosted, fast-path dequeue")
		q.tryBackpressureDequeue(ctx)
		return
	}

	klog.V(4).Infof("[SessionBoostQueue] grace: starting grace period %v", q.config.SessionBoostGracePeriod)
	timer := time.NewTimer(q.config.SessionBoostGracePeriod)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-q.notifyCh:
			if q.isHeadSessionBoosted() {
				klog.V(2).Info("[SessionBoostQueue] *** BOOSTED *** grace period: session-boosted request arrived, dequeuing immediately")
				q.tryBackpressureDequeue(ctx)
				return
			}
		case <-timer.C:
			q.tryBackpressureDequeue(ctx)
			return
		}
	}
}

// tryBackpressureDequeue admits as many queued requests as possible in one pass,
// stopping when the inflight limit is reached, backends report no capacity, or
// the queue is empty. This avoids the one-request-per-tick bottleneck during
// initial ramp-up and whenever spare capacity exists.
//
// Burst limiting (cross-pass): When BackendWaitingTolerance is configured, total
// dispatches within one MetricsScrapeInterval window are capped at
// podCount * (tolerance + 1). This prevents over-dispatching due to stale metrics:
// between two metric scrapes, the backendChecker reads stale RequestWaitingNum
// values that don't yet reflect recently dispatched requests. The burst counter
// resets when a full scrape interval elapses OR when backendChecker returns false
// (meaning metrics caught up and confirmed the backend is busy).
func (q *SessionBoostQueue) tryBackpressureDequeue(ctx context.Context) {
	perPod := q.config.InflightPerPod
	if perPod <= 0 {
		perPod = 1
	}
	maxInflight := int64(perPod)
	podCount := 0
	if q.podCounter != nil {
		podCount = q.podCounter()
		if podCount > 0 {
			maxInflight = int64(podCount) * int64(perPod)
		}
	}

	// Calculate burst limit: max dispatches within one metrics scrape window.
	// This accounts for stale metrics between scrapes.
	burstLimit := int64(0) // 0 means unlimited (no tolerance-based limiting)
	tolerance := q.config.BackendWaitingTolerance
	if tolerance >= 0 && q.backendChecker != nil {
		pods := podCount
		if pods <= 0 {
			pods = 1
		}
		burstLimit = int64(pods) * int64(tolerance+1)
		if burstLimit < 1 {
			burstLimit = 1
		}
	}

	// Reset burst counter if the metrics scrape window has elapsed,
	// meaning fresh metrics should now be available.
	if burstLimit > 0 {
		scrapeWindow := q.config.MetricsScrapeInterval
		if scrapeWindow <= 0 {
			scrapeWindow = 1 * time.Second
		}
		now := time.Now().UnixNano()
		windowStart := q.burstWindowStart.Load()
		if windowStart == 0 || now-windowStart >= int64(scrapeWindow) {
			q.burstDispatched.Store(0)
			q.burstWindowStart.Store(now)
		}
	}

	for {
		currentInflight := q.inflightCount.Load()

		if currentInflight >= maxInflight {
			q.logBackpressureThrottled("inflight limit reached, inflight=%d maxInflight=%d pods=%d perPod=%d",
				currentInflight, maxInflight, podCount, perPod)
			return
		}

		// Burst limit: stop dispatching if we've exhausted the budget for this
		// metrics scrape window. Next dispatch will be allowed after metrics refresh.
		if burstLimit > 0 && q.burstDispatched.Load() >= burstLimit {
			q.logBackpressureThrottled("burst limit reached, dispatched=%d burstLimit=%d scrapeWindow=%v",
				q.burstDispatched.Load(), burstLimit, q.config.MetricsScrapeInterval)
			return
		}

		if !q.backendChecker() {
			// Backend reports busy — metrics have caught up with our dispatches.
			// Reset burst counter so we get a fresh budget on next availability.
			q.burstDispatched.Store(0)
			q.burstWindowStart.Store(time.Now().UnixNano())
			q.mu.RLock()
			queueLen := q.heap.Len()
			q.mu.RUnlock()
			q.logBackpressureThrottled("backend pods busy, holding dequeue. queueLen=%d inflight=%d pods=%d",
				queueLen, currentInflight, podCount)
			return
		}

		q.mu.RLock()
		queueLen := q.heap.Len()
		q.mu.RUnlock()
		if queueLen == 0 {
			return
		}

		req, err := q.popWhenAvailable(ctx)
		if err != nil || req == nil {
			return
		}

		q.inflightCount.Add(1)
		q.burstDispatched.Add(1)
		releaseOnce := sync.Once{}
		req.Release = func() {
			releaseOnce.Do(func() {
				q.inflightCount.Add(-1)
				// Decrement burst counter: the backend absorbed this request,
				// so it no longer contributes to stale-metrics over-dispatch risk.
				if q.burstDispatched.Load() > 0 {
					q.burstDispatched.Add(-1)
				}
				select {
				case q.releaseCh <- struct{}{}:
				default:
				}
				if q.metrics != nil {
					q.metrics.DecSessionBoostQueueInflight(req.ModelName)
				}
			})
		}
		if q.metrics != nil {
			q.metrics.IncSessionBoostQueueInflight(req.ModelName)
		}

		if req.SessionBoost {
			klog.V(2).Infof("[SessionBoostQueue] *** BOOSTED DEQUEUE *** reqID=%s user=%s model=%s sessionID=%s reason=session_cache_hit inflight=%d/%d",
				req.ReqID, req.UserID, req.ModelName, req.SessionID, q.inflightCount.Load(), maxInflight)
		} else if q.config.EnableWaitPromotion && q.config.MaxWaitBeforePromotion > 0 && time.Since(req.RequestTime) >= q.config.MaxWaitBeforePromotion {
			klog.V(2).Infof("[SessionBoostQueue] *** PROMOTED DEQUEUE *** reqID=%s user=%s model=%s reason=wait_timeout waited=%v inflight=%d/%d",
				req.ReqID, req.UserID, req.ModelName, time.Since(req.RequestTime).Round(time.Millisecond), q.inflightCount.Load(), maxInflight)
		} else {
			klog.V(4).Infof("[SessionBoostQueue] dequeue: reqID=%s user=%s model=%s inflight=%d/%d",
				req.ReqID, req.UserID, req.ModelName, q.inflightCount.Load(), maxInflight)
		}

		if req.NotifyChan != nil {
			close(req.NotifyChan)
		}
	}
}

// backpressureLogInterval is the minimum interval between repeated backpressure log messages.
const backpressureLogInterval = 5 * time.Second

// logBackpressureThrottled logs a backpressure message at most once per backpressureLogInterval.
func (q *SessionBoostQueue) logBackpressureThrottled(format string, args ...interface{}) {
	now := time.Now().UnixNano()
	last := q.lastBackpressureLog.Load()
	if now-last < int64(backpressureLogInterval) {
		return
	}
	if q.lastBackpressureLog.CompareAndSwap(last, now) {
		klog.V(4).Infof("[SessionBoostQueue] backpressure: "+format, args...)
	}
}

// Close stops the dequeue loop and drains pending items.
func (q *SessionBoostQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	select {
	case <-q.stopCh:
		return
	default:
		close(q.stopCh)
	}

	for q.heap.Len() > 0 {
		req := heap.Pop(&q.heap).(*Request)
		if q.metrics != nil {
			q.metrics.DecSessionBoostQueueSize(req.ModelName)
		}
	}
	klog.V(4).Info("[SessionBoostQueue] queue closed and drained")
}

// MarkSessionCompleted records that a request from the given session has completed.
func (q *SessionBoostQueue) MarkSessionCompleted(sessionID string) {
	q.sessionTracker.MarkCompleted(sessionID)
}

// GetSessionTracker returns the session tracker.
func (q *SessionBoostQueue) GetSessionTracker() *SessionTracker {
	return q.sessionTracker
}

// SetPodCounter sets the function used to determine the number of ready backend pods.
func (q *SessionBoostQueue) SetPodCounter(counter func() int) {
	q.podCounter = counter
}

// GetInflightCount returns the current number of inflight requests.
func (q *SessionBoostQueue) GetInflightCount() int64 {
	return q.inflightCount.Load()
}

// Len returns the current queue length.
func (q *SessionBoostQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.heap.Len()
}
