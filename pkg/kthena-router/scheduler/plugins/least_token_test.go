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
)

func makePodWithTokens(name string, tokens int64) *datastore.PodInfo {
	p := &datastore.PodInfo{Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}}}
	p.SetOnFlightInputTokenNum(tokens)
	return p
}

func TestLeastTokenScore(t *testing.T) {
	tests := []struct {
		name           string
		pods           []*datastore.PodInfo
		expectedScores map[string]int
	}{
		{
			name: "all pods idle",
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 0),
				makePodWithTokens("pod-2", 0),
				makePodWithTokens("pod-3", 0),
			},
			expectedScores: map[string]int{"pod-1": 100, "pod-2": 100, "pod-3": 100},
		},
		{
			name: "single pod idle",
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 0),
			},
			expectedScores: map[string]int{"pod-1": 100},
		},
		{
			name: "mixed token load",
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 0),
				makePodWithTokens("pod-2", 1000),
				makePodWithTokens("pod-3", 500),
			},
			expectedScores: map[string]int{"pod-1": 100, "pod-2": 0, "pod-3": 50},
		},
		{
			name: "normal non-zero case",
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 100),
				makePodWithTokens("pod-2", 200),
				makePodWithTokens("pod-3", 300),
			},
			expectedScores: map[string]int{"pod-1": 66, "pod-2": 33, "pod-3": 0},
		},
		{
			name:           "empty pod list",
			pods:           []*datastore.PodInfo{},
			expectedScores: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewLeastToken(runtime.RawExtension{Raw: []byte(`{}`)})
			scores := plugin.Score(nil, tt.pods)

			for _, pod := range tt.pods {
				podName := pod.Pod.Name
				expected := tt.expectedScores[podName]
				actual := scores[pod]
				if actual != expected {
					t.Errorf("pod %s: expected score %d, got %d", podName, expected, actual)
				}
			}
		})
	}
}

func TestLeastTokenFilter(t *testing.T) {
	tests := []struct {
		name              string
		maxOnFlightTokens int64
		pods              []*datastore.PodInfo
		expectedNames     []string
	}{
		{
			name:              "all pods under limit",
			maxOnFlightTokens: 10000,
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 1000),
				makePodWithTokens("pod-2", 5000),
				makePodWithTokens("pod-3", 9000),
			},
			expectedNames: []string{"pod-1", "pod-2", "pod-3"},
		},
		{
			name:              "one pod over limit",
			maxOnFlightTokens: 10000,
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 1000),
				makePodWithTokens("pod-2", 15000),
				makePodWithTokens("pod-3", 5000),
			},
			expectedNames: []string{"pod-1", "pod-3"},
		},
		{
			name:              "all pods over limit",
			maxOnFlightTokens: 1000,
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 2000),
				makePodWithTokens("pod-2", 5000),
			},
			expectedNames: []string{},
		},
		{
			name:              "pod at exact limit is filtered out",
			maxOnFlightTokens: 5000,
			pods: []*datastore.PodInfo{
				makePodWithTokens("pod-1", 5000),
				makePodWithTokens("pod-2", 4999),
			},
			expectedNames: []string{"pod-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &LeastToken{
				name:              LeastTokenPluginName,
				maxOnFlightTokens: tt.maxOnFlightTokens,
			}
			result := plugin.Filter(nil, tt.pods)

			if len(result) != len(tt.expectedNames) {
				t.Fatalf("expected %d pods, got %d", len(tt.expectedNames), len(result))
			}
			for i, pod := range result {
				if pod.Pod.Name != tt.expectedNames[i] {
					t.Errorf("index %d: expected pod %s, got %s", i, tt.expectedNames[i], pod.Pod.Name)
				}
			}
		})
	}
}

func TestLeastTokenName(t *testing.T) {
	plugin := NewLeastToken(runtime.RawExtension{Raw: []byte(`{}`)})
	if plugin.Name() != LeastTokenPluginName {
		t.Errorf("expected plugin name %q, got %q", LeastTokenPluginName, plugin.Name())
	}
}
