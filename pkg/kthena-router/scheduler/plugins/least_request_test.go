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

func makePodWithOnFlight(name string, onFlight int64) *datastore.PodInfo {
	p := &datastore.PodInfo{Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}}}
	p.SetOnFlightRequestNum(onFlight)
	return p
}

// makePodWithWaiting creates a PodInfo with an on-flight count and an
// engine-reported waiting-queue depth.
func makePodWithWaiting(name string, onFlight int64, waiting float64) *datastore.PodInfo {
	p := &datastore.PodInfo{
		Pod:               &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}},
		RequestWaitingNum: waiting,
	}
	p.SetOnFlightRequestNum(onFlight)
	return p
}

func TestLeastRequestScore(t *testing.T) {
	tests := []struct {
		name           string
		pods           []*datastore.PodInfo
		expectedScores map[string]int
	}{
		{
			name: "all pods idle",
			pods: []*datastore.PodInfo{
				makePodWithOnFlight("pod-1", 0),
				makePodWithOnFlight("pod-2", 0),
				makePodWithOnFlight("pod-3", 0),
			},
			expectedScores: map[string]int{"pod-1": 100, "pod-2": 100, "pod-3": 100},
		},
		{
			name: "single pod idle",
			pods: []*datastore.PodInfo{
				makePodWithOnFlight("pod-1", 0),
			},
			expectedScores: map[string]int{"pod-1": 100},
		},
		{
			name: "mixed on-flight load",
			pods: []*datastore.PodInfo{
				makePodWithOnFlight("pod-1", 0),
				makePodWithOnFlight("pod-2", 10),
				makePodWithOnFlight("pod-3", 5),
			},
			expectedScores: map[string]int{"pod-1": 100, "pod-2": 0, "pod-3": 50},
		},
		{
			name: "normal non-zero case",
			pods: []*datastore.PodInfo{
				makePodWithOnFlight("pod-1", 1),
				makePodWithOnFlight("pod-2", 2),
				makePodWithOnFlight("pod-3", 3),
			},
			expectedScores: map[string]int{"pod-1": 66, "pod-2": 33, "pod-3": 0},
		},
		{
			// Demonstrates that the engine-reported waiting-queue depth dominates
			// scoring. pod-2 has 5 in-flight and 5 waiting → base = 5 + 4*5 = 25.
			// pod-1 has the same on-flight but no waiting → base = 5.
			// pod-3 is idle → base = 0.
			// Scores: pod-3=100, pod-1=int(20/25*100)=80, pod-2=0.
			name: "waiting queue dominates score",
			pods: []*datastore.PodInfo{
				makePodWithWaiting("pod-1", 5, 0), // onFlight=5, waiting=0 → base=5
				makePodWithWaiting("pod-2", 5, 5), // onFlight=5, waiting=5 → base=25
				makePodWithWaiting("pod-3", 0, 0), // idle → base=0
			},
			expectedScores: map[string]int{"pod-1": 80, "pod-2": 0, "pod-3": 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewLeastRequest(runtime.RawExtension{Raw: []byte(`{}`)})
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
