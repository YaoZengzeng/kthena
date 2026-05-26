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
	"istio.io/istio/pkg/slices"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
	"github.com/volcano-sh/kthena/pkg/kthena-router/scheduler/framework"
)

const LeastRequestPluginName = "least-request"

var _ framework.ScorePlugin = &LeastRequest{}
var _ framework.FilterPlugin = &LeastRequest{}

type LeastRequest struct {
	name               string
	maxWaitingRequests int
}

type LeastRequestArgs struct {
	// MaxWaitingRequests filters out pods whose engine-reported waiting-queue
	// depth exceeds this value. It captures backpressure that the router cannot
	// observe directly (e.g. requests queued inside the engine before execution).
	MaxWaitingRequests int `yaml:"maxWaitingRequests,omitempty"`
}

func NewLeastRequest(pluginArg runtime.RawExtension) *LeastRequest {
	var leastRequestArgs LeastRequestArgs
	if pluginArg.Raw == nil || yaml.Unmarshal(pluginArg.Raw, &leastRequestArgs) != nil {
		klog.Errorf("Unmarshal LeastRequestArgs error, setting default value")
		leastRequestArgs = LeastRequestArgs{
			MaxWaitingRequests: 10,
		}
	}
	if leastRequestArgs.MaxWaitingRequests == 0 {
		leastRequestArgs.MaxWaitingRequests = 10
	}

	return &LeastRequest{
		name:               LeastRequestPluginName,
		maxWaitingRequests: leastRequestArgs.MaxWaitingRequests,
	}
}

func (l *LeastRequest) Name() string {
	return l.name
}

func (l *LeastRequest) Filter(ctx *framework.Context, pods []*datastore.PodInfo) []*datastore.PodInfo {
	return slices.FilterInPlace(pods, func(info *datastore.PodInfo) bool {
		waiting := info.GetRequestWaitingNum()
		pass := waiting < float64(l.maxWaitingRequests)
		if !pass {
			klog.V(4).Infof("[least-request] Filter OUT pod %s/%s: waitingNum=%.1f >= maxWaiting=%d",
				info.Pod.Namespace, info.Pod.Name, waiting, l.maxWaitingRequests)
		}
		return pass
	})
}

func (l *LeastRequest) Score(ctx *framework.Context, pods []*datastore.PodInfo) map[*datastore.PodInfo]int {
	scoreResults := make(map[*datastore.PodInfo]int)
	if len(pods) == 0 {
		return scoreResults
	}

	// Score formula: base = onFlight + 4 * waiting
	//
	//   - onFlight: router-tracked in-flight count, updated with zero delay.
	//     Acts as a proxy for current pod load and avoids the ~1 s engine-metrics
	//     poll lag.
	//   - waiting: engine-reported waiting-queue depth from metrics. Weighted ×4
	//     to strongly penalise pods whose queue is building up.
	baseScores := make(map[*datastore.PodInfo]float64)
	maxScore := 0.0
	for _, info := range pods {
		onFlight := float64(info.GetOnFlightRequestNum())
		waiting := info.GetRequestWaitingNum()
		base := onFlight + 4*waiting
		baseScores[info] = base
		if base > maxScore {
			maxScore = base
		}
		klog.V(4).Infof("[least-request] Score pod %s/%s: onFlight=%.0f, waiting=%.0f, base=%.1f",
			info.Pod.Namespace, info.Pod.Name, onFlight, waiting, base)
	}

	// Normalise to [0, 100]: the least-loaded pod gets 100.
	for _, info := range pods {
		score := 100.0
		if maxScore > 0 {
			score = ((maxScore - baseScores[info]) / maxScore) * 100
		}
		scoreResults[info] = int(score)
		klog.V(4).Infof("[least-request] Final score pod %s/%s: base=%.1f, maxBase=%.1f, normalizedScore=%d",
			info.Pod.Namespace, info.Pod.Name, baseScores[info], maxScore, int(score))
	}

	return scoreResults
}
