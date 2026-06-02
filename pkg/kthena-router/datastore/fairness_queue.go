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
// has an empty waiting queue (i.e. RequestWaitingNum == 0), meaning the backend
// can accept a new request without queuing.
type BackendWaitingChecker func() bool

// FairnessQueueConfig holds configurable parameters for the fairness queue.
type FairnessQueueConfig struct {
	// MaxConcurrent is the maximum number of in-flight requests allowed through
	// the fairness gate for this model. When 0, falls back to backpressure-based dequeue.
	MaxConcurrent int

	// MaxQPS is the upper-bound dequeue rate used only in ticker/QPS mode (legacy).
	MaxQPS int

	// BackpressurePollInterval controls how often the backpressure checker polls
	// backend pod waiting queue status. Defaults to 100ms.
	BackpressurePollInterval time.Duration

	// MaxPriorityRefreshRetries bounds refresh-and-reinsert loops before a heap rebuild.
	// 0 disables dequeue-time refresh (current behavior).
	MaxPriorityRefreshRetries int

	// RebuildThreshold controls when to refresh all queued priorities and rebuild the heap.
	RebuildThreshold int

	// TokenWeight is the token-usage weight in the composite priority score.
	TokenWeight float64

	// RequestNumWeight is the request-count weight in the composite priority score.
	RequestNumWeight float64

	// SessionBoostEnabled enables session-aware priority boosting for multi-turn conversations.
	// When enabled, requests from sessions that recently completed a request will be boosted
	// to the front of the queue to leverage prefix cache.
	SessionBoostEnabled bool

	// SessionBoostTTL is the duration after which a session boost expires.
	// Requests from the same session (identified by X-Correlation-ID) that arrive
	// within this window after the previous request completed will be boosted.
	SessionBoostTTL time.Duration

	// InflightPerPod is the maximum number of inflight requests allowed per backend pod
	// in backpressure mode. The total inflight limit is InflightPerPod * podCount.
	// Defaults to 1.
	InflightPerPod int

	// SessionBoostGracePeriod is the duration to wait after a release before dequeuing
	// the next request in backpressure mode when session boost is enabled.
	// This gives the same session time to submit a follow-up request that benefits
	// from prefix cache, rather than immediately dispatching an unrelated request.
	// If a session-boosted request arrives during this window, it is dequeued immediately.
	// Defaults to 50ms. Set to 0 to disable the grace period.
	SessionBoostGracePeriod time.Duration
}

// DefaultFairnessQueueConfig returns backward-compatible defaults.
func DefaultFairnessQueueConfig() FairnessQueueConfig {
	return FairnessQueueConfig{
		MaxConcurrent:             0,
		MaxQPS:                    100,
		MaxPriorityRefreshRetries: 0,
		RebuildThreshold:          64,
		TokenWeight:               1.0,
		RequestNumWeight:          0.0,
		SessionBoostEnabled:       false,
		SessionBoostTTL:           60 * time.Second,
		BackpressurePollInterval:  100 * time.Millisecond,
		InflightPerPod:            1,
		SessionBoostGracePeriod:   50 * time.Millisecond,
	}
}

type FairnessPrioritySource interface {
	GetTokenCount(user, model string) (float64, error)
	GetRequestCount(user, model string) (int, error)
}

func CalculateFairnessPriority(source FairnessPrioritySource, userID, modelName string, tokenWeight, requestNumWeight float64) (float64, error) {
	tokenCount, err := source.GetTokenCount(userID, modelName)
	if err != nil {
		return 0, err
	}

	priority := tokenWeight * tokenCount
	if requestNumWeight == 0 {
		return priority, nil
	}

	requestCount, err := source.GetRequestCount(userID, modelName)
	if err != nil {
		return 0, err
	}

	return priority + requestNumWeight*float64(requestCount), nil
}

// Request represents a request item in the priority queue
type Request struct {
	ReqID         string
	UserID        string  // User ID for fairness scheduling
	ModelName     string  // Target model for per-model fair queuing
	CorrelationID string  // Session identifier from X-Correlation-ID header for multi-turn conversations
	Priority      float64 // Priority (lower value means higher priority)
	SessionBoost  bool    // Whether this request has session priority boost (recently completed session)
	RequestTime   time.Time
	NotifyChan    chan struct{}
	CancelCh      <-chan struct{} // Request-scoped cancellation signal
	Release       func()          // Set by the queue when a permit is acquired
}

// SessionTracker tracks recently completed sessions for priority boosting.
// It maps correlation IDs to their last completion time, allowing follow-up
// requests in the same session to be prioritized for prefix cache utilization.
type SessionTracker struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // correlationID -> last completion time
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
func (st *SessionTracker) MarkCompleted(correlationID string) {
	if correlationID == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[correlationID] = time.Now()
}

// HasRecentCompletion checks if the given correlation ID has a completion within the TTL window.
func (st *SessionTracker) HasRecentCompletion(correlationID string) bool {
	if correlationID == "" {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	completionTime, exists := st.sessions[correlationID]
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

// RequestPriorityQueue implements the heap.Interface
type RequestPriorityQueue struct {
	stopCh   chan struct{}    // Context for cancellation
	notifyCh chan struct{}    // Channel for item availability notification
	mu       sync.RWMutex     // Ensure concurrent safety with read/write locks
	heap     []*Request       // Underlying storage structure
	metrics  *metrics.Metrics // Metrics instance for recording queue stats

	// Backpressure-aware dequeue (Phase 2)
	sem    chan struct{} // Semaphore for capacity-based admission; nil means backpressure mode
	config FairnessQueueConfig

	// Priority refresh (Phase 2)
	tokenTracker TokenTracker // Optional; when set, enables dequeue-time priority refresh

	// Session-aware priority boosting for multi-turn conversations
	sessionTracker *SessionTracker

	// backendChecker checks if backend pods have capacity (waiting queue empty).
	// Used in backpressure mode to gate dequeue on backend readiness.
	backendChecker BackendWaitingChecker

	// Inflight tracking for backpressure mode: prevents flooding backends between metric scrapes.
	// inflightCount tracks requests dequeued but not yet completed.
	inflightCount atomic.Int64
	// releaseCh is signaled when an inflight request completes, enabling immediate dequeue.
	releaseCh chan struct{}
	// podCounter returns the number of ready backend pods. Used to cap inflight at 1 per pod.
	podCounter func() int
}

var _ heap.Interface = &RequestPriorityQueue{}

// NewRequestPriorityQueue creates a new priority queue. Pass nil metrics to use defaults.
func NewRequestPriorityQueue(metricsInstance *metrics.Metrics) *RequestPriorityQueue {
	return NewRequestPriorityQueueWithConfig(metricsInstance, DefaultFairnessQueueConfig(), nil)
}

// NewRequestPriorityQueueWithConfig creates a priority queue with explicit configuration.
func NewRequestPriorityQueueWithConfig(metricsInstance *metrics.Metrics, cfg FairnessQueueConfig, tracker TokenTracker, checker ...BackendWaitingChecker) *RequestPriorityQueue {
	if metricsInstance == nil {
		metricsInstance = metrics.DefaultMetrics
	}
	if cfg.TokenWeight == 0 && cfg.RequestNumWeight == 0 {
		cfg.TokenWeight = DefaultFairnessQueueConfig().TokenWeight
	}
	pq := &RequestPriorityQueue{
		stopCh:       make(chan struct{}),
		notifyCh:     make(chan struct{}, 1), // Buffered to prevent blocking
		releaseCh:    make(chan struct{}, 1), // Buffered for release notification
		heap:         make([]*Request, 0),
		metrics:      metricsInstance,
		config:       cfg,
		tokenTracker: tracker,
	}
	if cfg.MaxConcurrent > 0 {
		pq.sem = make(chan struct{}, cfg.MaxConcurrent)
	}
	if cfg.SessionBoostEnabled {
		pq.sessionTracker = NewSessionTracker(cfg.SessionBoostTTL)
	}
	if len(checker) > 0 && checker[0] != nil {
		pq.backendChecker = checker[0]
	}
	return pq
}

// Implement heap.Interface methods
func (pq *RequestPriorityQueue) Len() int { return len(pq.heap) }

func (pq *RequestPriorityQueue) Less(i, j int) bool {
	// Session-boosted requests always take priority over non-boosted ones.
	// This ensures multi-turn conversation follow-up requests are processed first
	// to maximize prefix cache hit rate.
	if pq.heap[i].SessionBoost != pq.heap[j].SessionBoost {
		return pq.heap[i].SessionBoost
	}
	// Among boosted requests (or among non-boosted), use FIFO for same user
	if pq.heap[i].UserID == pq.heap[j].UserID {
		return pq.heap[i].RequestTime.Before(pq.heap[j].RequestTime)
	}
	// different users, compare priority, actually token usage here
	if pq.heap[i].Priority != pq.heap[j].Priority {
		return pq.heap[i].Priority < pq.heap[j].Priority
	}
	// When priorities are equal, compare request arrival times: earlier times have higher priority
	return pq.heap[i].RequestTime.Before(pq.heap[j].RequestTime)
}

func (pq *RequestPriorityQueue) Swap(i, j int) {
	pq.heap[i], pq.heap[j] = pq.heap[j], pq.heap[i]
}

func (pq *RequestPriorityQueue) Push(x interface{}) {
	item := x.(*Request)
	pq.heap = append(pq.heap, item)
}

func (pq *RequestPriorityQueue) Pop() interface{} {
	n := len(pq.heap)
	if n == 0 {
		return nil
	}
	item := pq.heap[n-1]
	pq.heap[n-1] = nil
	pq.heap = pq.heap[0 : n-1]
	return item
}

func (pq *RequestPriorityQueue) PushRequest(r *Request) error {
	pq.mu.Lock()

	// Check session boost: if this request's correlation ID has a recent completion,
	// mark it as boosted so it gets priority in the queue.
	if pq.sessionTracker != nil && r.CorrelationID != "" {
		if pq.sessionTracker.HasRecentCompletion(r.CorrelationID) {
			r.SessionBoost = true
		}
	}

	heap.Push(pq, r)
	queueLen := len(pq.heap)
	pq.mu.Unlock()

	// Update fairness queue size metrics
	if pq.metrics != nil {
		pq.metrics.IncFairnessQueueSize(r.ModelName, r.UserID)
	}

	// Log only when session boost promotes a request to the head
	if r.SessionBoost {
		klog.V(4).Infof("[FairnessQueue] session boost: reqID=%s correlationID=%s promoted to head, queueLen=%d",
			r.ReqID, r.CorrelationID, queueLen)
	}

	// Signal that a new item is available
	select {
	case pq.notifyCh <- struct{}{}:
	default: // Channel is full, notification already pending
	}
	return nil
}

// popWhenAvailable blocks until an item is available or the context is done, then pops one item.
// Cancelled/timed-out requests are skipped automatically.
func (pq *RequestPriorityQueue) popWhenAvailable(ctx context.Context) (*Request, error) {
	refreshRetries := 0
	for {
		pq.mu.Lock()
		if len(pq.heap) > 0 {
			req := heap.Pop(pq).(*Request)

			// Skip cancelled/timed-out requests
			if req.isCancelled() {
				if pq.metrics != nil {
					pq.metrics.DecFairnessQueueSize(req.ModelName, req.UserID)
					queueDuration := time.Since(req.RequestTime)
					pq.metrics.RecordFairnessQueueDuration(req.ModelName, req.UserID, queueDuration)
					pq.metrics.IncFairnessQueueCancelled(req.ModelName, req.UserID)
				}
				pq.mu.Unlock()
				continue
			}

			// Bounded priority refresh: re-evaluate root priority at dequeue time
			if pq.tokenTracker != nil && pq.config.MaxPriorityRefreshRetries > 0 {
				newPri, err := CalculateFairnessPriority(
					pq.tokenTracker,
					req.UserID,
					req.ModelName,
					pq.config.TokenWeight,
					pq.config.RequestNumWeight,
				)
				if err == nil && newPri != req.Priority {
					req.Priority = newPri
					// Check if this request should still be dequeued
					if len(pq.heap) > 0 && newPri > pq.heap[0].Priority {
						refreshRetries++
						if pq.metrics != nil {
							pq.metrics.IncFairnessQueuePriorityRefresh(req.ModelName)
						}
						if refreshRetries >= pq.config.MaxPriorityRefreshRetries {
							heap.Push(pq, req)
							if pq.shouldRebuildLocked() {
								pq.rebuildHeap()
								if pq.metrics != nil {
									pq.metrics.IncFairnessQueueHeapRebuild(req.ModelName)
								}
							}
							refreshRetries = 0
							pq.mu.Unlock()
							continue
						}
						// Reinsert with updated priority and retry
						heap.Push(pq, req)
						pq.mu.Unlock()
						continue
					}
				}
			}

			queueDuration := time.Since(req.RequestTime)

			// Update fairness queue size metrics and record queue duration
			if pq.metrics != nil {
				pq.metrics.DecFairnessQueueSize(req.ModelName, req.UserID)
				pq.metrics.RecordFairnessQueueDuration(req.ModelName, req.UserID, queueDuration)
				pq.metrics.IncFairnessQueueDequeue(req.ModelName, req.UserID)
			}

			pq.mu.Unlock()

			return req, nil
		}
		pq.mu.Unlock()

		// Wait for notification or cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pq.stopCh:
			return nil, errors.New("queue stopped")
		case <-pq.notifyCh:
			// An item might be available, loop back to check
			continue
		}
	}
}

func (r *Request) isCancelled() bool {
	if r == nil || r.CancelCh == nil {
		return false
	}
	select {
	case <-r.CancelCh:
		return true
	default:
		return false
	}
}

func (pq *RequestPriorityQueue) shouldRebuildLocked() bool {
	return pq.config.RebuildThreshold <= 0 || len(pq.heap) <= pq.config.RebuildThreshold
}

// rebuildHeap refreshes priorities for all queued items and rebuilds the heap.
// Caller must hold pq.mu.
func (pq *RequestPriorityQueue) rebuildHeap() {
	if pq.tokenTracker == nil {
		return
	}
	for _, req := range pq.heap {
		if newPri, err := CalculateFairnessPriority(
			pq.tokenTracker,
			req.UserID,
			req.ModelName,
			pq.config.TokenWeight,
			pq.config.RequestNumWeight,
		); err == nil {
			req.Priority = newPri
		}
	}
	heap.Init(pq)
}

func (pq *RequestPriorityQueue) requeueRequest(req *Request) {
	if req == nil {
		return
	}
	pq.mu.Lock()
	heap.Push(pq, req)
	pq.mu.Unlock()
	select {
	case pq.notifyCh <- struct{}{}:
	default:
	}
}

// Run starts the dequeue loop. In semaphore mode (MaxConcurrent > 0), dequeue is
// gated by available capacity. In backpressure mode (backendChecker != nil), dequeue
// is gated by backend pod waiting queue emptiness. Otherwise, falls back to QPS-based
// ticker dequeue.
// Also starts a periodic session tracker cleanup goroutine if session boost is enabled.
func (pq *RequestPriorityQueue) Run(ctx context.Context, qps int) {
	// Start session tracker cleanup goroutine if enabled
	if pq.sessionTracker != nil {
		go pq.runSessionCleanup(ctx)
	}
	if pq.sem != nil {
		pq.runSemaphoreMode(ctx)
		return
	}
	if pq.backendChecker != nil {
		pq.runBackpressureMode(ctx)
		return
	}
	pq.runQPSMode(ctx, qps)
}

// runSessionCleanup periodically cleans up expired sessions from the session tracker.
func (pq *RequestPriorityQueue) runSessionCleanup(ctx context.Context) {
	cleanupInterval := pq.config.SessionBoostTTL
	if cleanupInterval < 10*time.Second {
		cleanupInterval = 10 * time.Second
	}
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pq.stopCh:
			return
		case <-ticker.C:
			pq.sessionTracker.Cleanup()
		}
	}
}

// runBackpressureMode dequeues requests only when backend pods have capacity.
// It uses two-level admission control:
//  1. Inflight limit: at most one inflight request per backend pod (prevents flooding
//     between metric scrapes).
//  2. Backend metrics check: at least one pod reports RequestWaitingNum == 0.
//
// When an inflight request completes (Release is called), the queue immediately
// attempts to dequeue the next request without waiting for the next tick.
//
// Session Grace Period: When session boost is enabled and SessionBoostGracePeriod > 0,
// a release event triggers a short wait before dequeuing. This gives the same session
// time to submit a follow-up request that can leverage prefix cache. If a session-boosted
// request arrives during the grace period, it is dequeued immediately without waiting.
func (pq *RequestPriorityQueue) runBackpressureMode(ctx context.Context) {
	pollInterval := pq.config.BackpressurePollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	klog.V(4).Infof("[FairnessQueue] starting backpressure dequeue loop, poll_interval=%v, sessionBoostEnabled=%v, gracePeriod=%v",
		pollInterval, pq.config.SessionBoostEnabled, pq.config.SessionBoostGracePeriod)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pq.stopCh:
			return
		case <-pq.releaseCh:
			// A previous inflight request completed.
			// If session boost grace period is enabled, wait briefly for a
			// same-session follow-up request to arrive (higher prefix cache hit).
			if pq.config.SessionBoostEnabled && pq.config.SessionBoostGracePeriod > 0 {
				pq.waitGraceAndDequeue(ctx)
			} else {
				pq.tryBackpressureDequeue(ctx)
			}
		case <-ticker.C:
			pq.tryBackpressureDequeue(ctx)
		}
	}
}

// isHeadSessionBoosted checks if the highest-priority request in the queue has a session boost.
func (pq *RequestPriorityQueue) isHeadSessionBoosted() bool {
	pq.mu.RLock()
	defer pq.mu.RUnlock()
	if len(pq.heap) == 0 {
		return false
	}
	return pq.heap[0].SessionBoost
}

// waitGraceAndDequeue waits up to SessionBoostGracePeriod for a session-boosted
// request to arrive at the head of the queue. If one arrives, it dequeues immediately.
// If the grace period expires without a session-boosted arrival, it dequeues normally.
// If the queue head is already session-boosted, skips the wait entirely.
func (pq *RequestPriorityQueue) waitGraceAndDequeue(ctx context.Context) {
	// Fast path: head is already session-boosted, no need to wait.
	if pq.isHeadSessionBoosted() {
		klog.V(4).Info("[FairnessQueue] grace: head already boosted, skipping wait")
		pq.tryBackpressureDequeue(ctx)
		return
	}

	klog.V(4).Infof("[FairnessQueue] grace: starting grace period %v", pq.config.SessionBoostGracePeriod)
	timer := time.NewTimer(pq.config.SessionBoostGracePeriod)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pq.stopCh:
			return
		case <-pq.notifyCh:
			// A new request was pushed; check if the head is now session-boosted.
			if pq.isHeadSessionBoosted() {
				klog.V(4).Info("[FairnessQueue] grace period: session-boosted request arrived, dequeuing immediately")
				pq.tryBackpressureDequeue(ctx)
				return
			}
			// Not session-boosted; continue waiting for grace period to expire.
		case <-timer.C:
			// Grace period expired; dequeue whatever is at the head.
			pq.tryBackpressureDequeue(ctx)
			return
		}
	}
}

// tryBackpressureDequeue attempts to dequeue one request if both the inflight limit
// and the backend capacity check pass.
func (pq *RequestPriorityQueue) tryBackpressureDequeue(ctx context.Context) {
	// Determine inflight limit based on pod count and per-pod allowance.
	perPod := pq.config.InflightPerPod
	if perPod <= 0 {
		perPod = 1
	}
	maxInflight := int64(perPod)
	podCount := 0
	if pq.podCounter != nil {
		podCount = pq.podCounter()
		if podCount > 0 {
			maxInflight = int64(podCount) * int64(perPod)
		}
	}

	currentInflight := pq.inflightCount.Load()

	// Primary gate: inflight limit prevents flooding between metric scrapes.
	if currentInflight >= maxInflight {
		klog.V(4).Infof("[FairnessQueue] backpressure: inflight limit reached, inflight=%d maxInflight=%d pods=%d perPod=%d",
			currentInflight, maxInflight, podCount, perPod)
		return
	}

	// Secondary gate: backend metrics confirm at least one pod has capacity.
	// Skip this check only when inflight==0 AND podCount==0 (no pods registered yet).
	if !pq.backendChecker() {
		pq.logBackendWaitingStatus(currentInflight, podCount)
		return
	}

	// Check queue has items.
	pq.mu.RLock()
	queueLen := len(pq.heap)
	pq.mu.RUnlock()
	if queueLen == 0 {
		return
	}

	req, err := pq.popWhenAvailable(ctx)
	if err != nil {
		return
	}
	if req == nil {
		return
	}

	// Track inflight and set Release for feedback loop.
	pq.inflightCount.Add(1)
	releaseOnce := sync.Once{}
	req.Release = func() {
		releaseOnce.Do(func() {
			pq.inflightCount.Add(-1)
			// Signal the dequeue loop that a slot is free.
			select {
			case pq.releaseCh <- struct{}{}:
			default:
			}
			if pq.metrics != nil {
				pq.metrics.DecFairnessQueueInflight(req.ModelName)
			}
		})
	}
	if pq.metrics != nil {
		pq.metrics.IncFairnessQueueInflight(req.ModelName)
	}

	klog.V(4).Infof("[FairnessQueue] backpressure dequeue: reqID=%s user=%s model=%s sessionBoost=%v inflight=%d/%d",
		req.ReqID, req.UserID, req.ModelName, req.SessionBoost, pq.inflightCount.Load(), maxInflight)

	if req.NotifyChan != nil {
		close(req.NotifyChan)
	}
}

// runQPSMode is the original fixed-rate ticker dequeue loop (legacy fallback).
func (pq *RequestPriorityQueue) runQPSMode(ctx context.Context, qps int) {
	if qps <= 0 {
		qps = 1
	}
	klog.V(4).Infof("[FairnessQueue] starting QPS dequeue loop (legacy), qps=%d", qps)
	interval := time.Second / time.Duration(qps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pq.stopCh:
			return
		case <-ticker.C:
			req, err := pq.popWhenAvailable(ctx)
			if err != nil {
				return
			}
			if req != nil && req.NotifyChan != nil {
				close(req.NotifyChan)
			}
		}
	}
}

// runSemaphoreMode dequeues based on available backend capacity only.
func (pq *RequestPriorityQueue) runSemaphoreMode(ctx context.Context) {
	for {
		req, err := pq.popWhenAvailable(ctx)
		if err != nil {
			return
		}
		if req == nil || req.NotifyChan == nil {
			continue
		}
		if req.isCancelled() {
			continue
		}

		select {
		case <-ctx.Done():
			pq.requeueRequest(req)
			return
		case <-pq.stopCh:
			pq.requeueRequest(req)
			return
		case pq.sem <- struct{}{}:
			// Permit acquired
		}

		releaseOnce := sync.Once{}
		trackedInflight := false
		req.Release = func() {
			releaseOnce.Do(func() {
				<-pq.sem
				if trackedInflight && pq.metrics != nil {
					pq.metrics.DecFairnessQueueInflight(req.ModelName)
				}
			})
		}

		if pq.metrics != nil {
			pq.metrics.IncFairnessQueueInflight(req.ModelName)
		}
		trackedInflight = true
		close(req.NotifyChan)
	}
}

// Close stops the dequeue loop and drains pending items from the heap.
// Callers waiting on NotifyChan will detect cancellation via their request-scoped signal.
func (pq *RequestPriorityQueue) Close() {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	select {
	case <-pq.stopCh:
		// already closed
		return
	default:
		close(pq.stopCh)
	}

	// Drain pending items: clear metrics for each remaining request
	for len(pq.heap) > 0 {
		req := heap.Pop(pq).(*Request)
		if pq.metrics != nil {
			pq.metrics.DecFairnessQueueSize(req.ModelName, req.UserID)
		}
	}
	klog.V(4).Info("[FairnessQueue] queue closed and drained")
}

// MarkSessionCompleted records that a request from the given session has completed.
// This enables priority boosting for subsequent requests from the same session.
func (pq *RequestPriorityQueue) MarkSessionCompleted(correlationID string) {
	if pq.sessionTracker != nil {
		pq.sessionTracker.MarkCompleted(correlationID)
	}
}

// GetSessionTracker returns the session tracker, or nil if session boost is not enabled.
func (pq *RequestPriorityQueue) GetSessionTracker() *SessionTracker {
	return pq.sessionTracker
}

// SetPodCounter sets the function used to determine the number of ready backend pods.
// This is used in backpressure mode to limit inflight requests to at most one per pod,
// preventing flooding between metric scrapes.
func (pq *RequestPriorityQueue) SetPodCounter(counter func() int) {
	pq.podCounter = counter
}

// GetInflightCount returns the current number of inflight requests (dequeued but not released).
func (pq *RequestPriorityQueue) GetInflightCount() int64 {
	return pq.inflightCount.Load()
}

// logBackendWaitingStatus logs the backend waiting status when backpressure blocks dequeue.
func (pq *RequestPriorityQueue) logBackendWaitingStatus(currentInflight int64, podCount int) {
	pq.mu.RLock()
	queueLen := len(pq.heap)
	pq.mu.RUnlock()

	klog.V(4).Infof("[FairnessQueue] backpressure: backend pods busy, holding dequeue. "+
		"queueLen=%d inflight=%d pods=%d",
		queueLen, currentInflight, podCount)
}
