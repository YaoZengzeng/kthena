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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
	"github.com/volcano-sh/kthena/pkg/kthena-router/scheduler/framework"
)

func makePod(namespace, name string) *datastore.PodInfo {
	return &datastore.PodInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
		},
	}
}

func TestSessionAffinityName(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	if sa.Name() != SessionAffinityPluginName {
		t.Errorf("expected name %q, got %q", SessionAffinityPluginName, sa.Name())
	}
}

func TestSessionAffinityScoreNoCorrelationID(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pods := []*datastore.PodInfo{
		makePod("ns", "pod-1"),
		makePod("ns", "pod-2"),
	}

	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "",
	}

	scores := sa.Score(ctx, pods)
	if len(scores) != 0 {
		t.Errorf("expected empty scores when no correlation ID, got %v", scores)
	}
}

func TestSessionAffinityScoreNoExistingSession(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pods := []*datastore.PodInfo{
		makePod("ns", "pod-1"),
		makePod("ns", "pod-2"),
	}

	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-123",
	}

	scores := sa.Score(ctx, pods)
	for _, pod := range pods {
		if score, ok := scores[pod]; !ok || score != 0 {
			t.Errorf("expected score 0 for pod %s with no existing session, got %d", pod.Pod.Name, score)
		}
	}
}

func TestSessionAffinityScoreWithExistingSession(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pods := []*datastore.PodInfo{
		makePod("ns", "pod-1"),
		makePod("ns", "pod-2"),
		makePod("ns", "pod-3"),
	}

	// Simulate a previous request that was routed to pod-2
	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-123",
		BestPods:      []*datastore.PodInfo{pods[1]},
	}
	sa.PostSchedule(ctx, 0)

	// Now score with the same correlation ID
	scores := sa.Score(ctx, pods)
	for _, pod := range pods {
		score := scores[pod]
		if pod.Pod.Name == "pod-2" {
			if score != 100 {
				t.Errorf("expected score 100 for affinity pod pod-2, got %d", score)
			}
		} else {
			if score != 0 {
				t.Errorf("expected score 0 for non-affinity pod %s, got %d", pod.Pod.Name, score)
			}
		}
	}
}

func TestSessionAffinityDifferentModels(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pod1 := makePod("ns", "pod-1")
	pod2 := makePod("ns", "pod-2")

	// Route session-123 to pod-1 for model-a
	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-123",
		BestPods:      []*datastore.PodInfo{pod1},
	}
	sa.PostSchedule(ctx, 0)

	// Score with same correlation ID but different model
	ctxB := &framework.Context{
		Model:         "model-b",
		CorrelationID: "session-123",
	}
	scores := sa.Score(ctxB, []*datastore.PodInfo{pod1, pod2})
	for _, pod := range []*datastore.PodInfo{pod1, pod2} {
		if score := scores[pod]; score != 0 {
			t.Errorf("expected score 0 for different model, got %d for %s", score, pod.Pod.Name)
		}
	}
}

func TestSessionAffinityPostScheduleUpdatesSession(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pod1 := makePod("ns", "pod-1")
	pod2 := makePod("ns", "pod-2")

	// First request routes to pod-1
	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-456",
		BestPods:      []*datastore.PodInfo{pod1},
	}
	sa.PostSchedule(ctx, 0)

	// Verify pod-1 gets 100
	scores := sa.Score(ctx, []*datastore.PodInfo{pod1, pod2})
	if scores[pod1] != 100 {
		t.Errorf("expected 100 for pod-1, got %d", scores[pod1])
	}
	if scores[pod2] != 0 {
		t.Errorf("expected 0 for pod-2, got %d", scores[pod2])
	}

	// Second request overrides to pod-2
	ctx2 := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-456",
		BestPods:      []*datastore.PodInfo{pod2},
	}
	sa.PostSchedule(ctx2, 0)

	// Verify pod-2 now gets 100
	scores = sa.Score(ctx2, []*datastore.PodInfo{pod1, pod2})
	if scores[pod2] != 100 {
		t.Errorf("expected 100 for pod-2 after update, got %d", scores[pod2])
	}
	if scores[pod1] != 0 {
		t.Errorf("expected 0 for pod-1 after update, got %d", scores[pod1])
	}
}

func TestSessionAffinityPostScheduleWithDecodePods(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pod1 := makePod("ns", "pod-1")
	pod2 := makePod("ns", "pod-2")

	// Simulate PD mode: PostSchedule with DecodePods
	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "session-pd",
		DecodePods:    []*datastore.PodInfo{pod1, pod2},
	}
	sa.PostSchedule(ctx, 0)

	// pod-1 should get 100 (index 0 of DecodePods)
	scores := sa.Score(ctx, []*datastore.PodInfo{pod1, pod2})
	if scores[pod1] != 100 {
		t.Errorf("expected 100 for decode pod-1, got %d", scores[pod1])
	}
	if scores[pod2] != 0 {
		t.Errorf("expected 0 for pod-2, got %d", scores[pod2])
	}
}

func TestSessionAffinityCustomTTL(t *testing.T) {
	args := runtime.RawExtension{Raw: []byte(`{"ttlSeconds": 1800, "maxSessions": 500}`)}
	sa := NewSessionAffinity(args)

	if sa.ttl.Seconds() != 1800 {
		t.Errorf("expected TTL 1800s, got %v", sa.ttl)
	}
	if sa.maxSessions != 500 {
		t.Errorf("expected maxSessions 500, got %d", sa.maxSessions)
	}
}

func TestSessionAffinityPostScheduleNoCorrelationID(t *testing.T) {
	sa := NewSessionAffinity(runtime.RawExtension{})
	pod1 := makePod("ns", "pod-1")

	// PostSchedule with empty correlation ID should not store anything
	ctx := &framework.Context{
		Model:         "model-a",
		CorrelationID: "",
		BestPods:      []*datastore.PodInfo{pod1},
	}
	sa.PostSchedule(ctx, 0)

	if len(sa.sessions) != 0 {
		t.Errorf("expected no sessions stored when correlation ID is empty, got %d", len(sa.sessions))
	}
}
