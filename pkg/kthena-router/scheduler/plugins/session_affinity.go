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

package plugins

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
	"github.com/volcano-sh/kthena/pkg/kthena-router/scheduler/framework"
)

const SessionAffinityPluginName = "session-affinity"

const (
	defaultSessionAffinityTTL         = 30 * time.Minute
	defaultSessionAffinityMaxSessions = 100000
)

var _ framework.ScorePlugin = &SessionAffinity{}
var _ framework.PostScheduleHook = &SessionAffinity{}

type SessionAffinity struct {
	name string

	ttl         time.Duration
	maxSessions int

	mu       sync.RWMutex
	sessions map[string]*sessionEntry // key: "model/correlationID"
}

type sessionEntry struct {
	podName   types.NamespacedName
	timestamp time.Time
}

type SessionAffinityArgs struct {
	// TTLSeconds is the time-to-live for session entries in seconds.
	// Sessions older than this are considered expired.
	TTLSeconds int `yaml:"ttlSeconds,omitempty"`
	// MaxSessions is the maximum number of session entries to keep.
	MaxSessions int `yaml:"maxSessions,omitempty"`
}

func NewSessionAffinity(pluginArg runtime.RawExtension) *SessionAffinity {
	args := SessionAffinityArgs{}
	if len(pluginArg.Raw) > 0 {
		if err := yaml.Unmarshal(pluginArg.Raw, &args); err != nil {
			klog.Errorf("Failed to unmarshal SessionAffinityArgs, using defaults: %v", err)
		}
	}

	ttl := defaultSessionAffinityTTL
	if args.TTLSeconds > 0 {
		ttl = time.Duration(args.TTLSeconds) * time.Second
	}

	maxSessions := defaultSessionAffinityMaxSessions
	if args.MaxSessions > 0 {
		maxSessions = args.MaxSessions
	}

	return &SessionAffinity{
		name:        SessionAffinityPluginName,
		ttl:         ttl,
		maxSessions: maxSessions,
		sessions:    make(map[string]*sessionEntry),
	}
}

func (s *SessionAffinity) Name() string {
	return s.name
}

func (s *SessionAffinity) Score(ctx *framework.Context, pods []*datastore.PodInfo) map[*datastore.PodInfo]int {
	scoreResults := make(map[*datastore.PodInfo]int, len(pods))
	if ctx.CorrelationID == "" {
		return scoreResults
	}

	key := sessionKey(ctx.Model, ctx.CorrelationID)

	s.mu.RLock()
	entry, exists := s.sessions[key]
	s.mu.RUnlock()

	if !exists || time.Since(entry.timestamp) > s.ttl {
		// No valid session entry: all pods get 0
		for _, pod := range pods {
			scoreResults[pod] = 0
		}
		return scoreResults
	}

	// Score: pod matching the session gets 100, others get 0
	for _, pod := range pods {
		nsName := types.NamespacedName{Namespace: pod.Pod.Namespace, Name: pod.Pod.Name}
		if nsName == entry.podName {
			scoreResults[pod] = 100
		} else {
			scoreResults[pod] = 0
		}
	}

	return scoreResults
}

func (s *SessionAffinity) PostSchedule(ctx *framework.Context, index int) {
	if ctx.CorrelationID == "" {
		return
	}

	var selectedPod *datastore.PodInfo
	if ctx.BestPods != nil && index < len(ctx.BestPods) {
		selectedPod = ctx.BestPods[index]
	} else if index < len(ctx.DecodePods) && ctx.DecodePods[index] != nil {
		selectedPod = ctx.DecodePods[index]
	}

	if selectedPod == nil || selectedPod.Pod == nil {
		return
	}

	key := sessionKey(ctx.Model, ctx.CorrelationID)
	podName := types.NamespacedName{Namespace: selectedPod.Pod.Namespace, Name: selectedPod.Pod.Name}

	s.mu.Lock()
	// Evict expired entries if we're at capacity
	if len(s.sessions) >= s.maxSessions {
		s.evictExpired()
	}
	s.sessions[key] = &sessionEntry{
		podName:   podName,
		timestamp: time.Now(),
	}
	s.mu.Unlock()
}

// evictExpired removes expired entries. Must be called with s.mu held.
func (s *SessionAffinity) evictExpired() {
	now := time.Now()
	for key, entry := range s.sessions {
		if now.Sub(entry.timestamp) > s.ttl {
			delete(s.sessions, key)
		}
	}
}

func sessionKey(model, correlationID string) string {
	return model + "/" + correlationID
}
