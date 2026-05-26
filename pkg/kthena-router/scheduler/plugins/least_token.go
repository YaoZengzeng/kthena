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

const LeastTokenPluginName = "least-token"

var _ framework.ScorePlugin = &LeastToken{}
var _ framework.FilterPlugin = &LeastToken{}

type LeastToken struct {
	name              string
	maxOnFlightTokens int64
}

type LeastTokenArgs struct {
	// MaxOnFlightTokens filters out pods whose in-flight input token count
	// exceeds this value. This prevents overloading a single backend with
	// too many tokens queued for processing.
	MaxOnFlightTokens int64 `yaml:"maxOnFlightTokens,omitempty"`
}

func NewLeastToken(pluginArg runtime.RawExtension) *LeastToken {
	var leastTokenArgs LeastTokenArgs
	if pluginArg.Raw == nil || yaml.Unmarshal(pluginArg.Raw, &leastTokenArgs) != nil {
		klog.Errorf("Unmarshal LeastTokenArgs error, setting default value")
		leastTokenArgs = LeastTokenArgs{
			MaxOnFlightTokens: 100000,
		}
	}
	if leastTokenArgs.MaxOnFlightTokens == 0 {
		leastTokenArgs.MaxOnFlightTokens = 100000
	}

	return &LeastToken{
		name:              LeastTokenPluginName,
		maxOnFlightTokens: leastTokenArgs.MaxOnFlightTokens,
	}
}

func (l *LeastToken) Name() string {
	return l.name
}

func (l *LeastToken) Filter(ctx *framework.Context, pods []*datastore.PodInfo) []*datastore.PodInfo {
	return slices.FilterInPlace(pods, func(info *datastore.PodInfo) bool {
		tokens := info.GetOnFlightInputTokenNum()
		pass := tokens < l.maxOnFlightTokens
		if !pass {
			klog.V(4).Infof("[least-token] Filter OUT pod %s/%s: onFlightInputTokens=%d >= maxOnFlightTokens=%d",
				info.Pod.Namespace, info.Pod.Name, tokens, l.maxOnFlightTokens)
		}
		return pass
	})
}

func (l *LeastToken) Score(ctx *framework.Context, pods []*datastore.PodInfo) map[*datastore.PodInfo]int {
	scoreResults := make(map[*datastore.PodInfo]int)
	if len(pods) == 0 {
		return scoreResults
	}

	// Score based on the number of in-flight input tokens per pod.
	// Pods with fewer in-flight tokens get higher scores.
	tokenCounts := make(map[*datastore.PodInfo]float64)
	maxTokens := 0.0
	for _, info := range pods {
		tokens := float64(info.GetOnFlightInputTokenNum())
		tokenCounts[info] = tokens
		if tokens > maxTokens {
			maxTokens = tokens
		}
		klog.V(4).Infof("[least-token] Score pod %s/%s: onFlightInputTokens=%.0f",
			info.Pod.Namespace, info.Pod.Name, tokens)
	}

	// Normalise to [0, 100]: the pod with fewest in-flight tokens gets 100.
	for _, info := range pods {
		score := 100.0
		if maxTokens > 0 {
			score = ((maxTokens - tokenCounts[info]) / maxTokens) * 100
		}
		scoreResults[info] = int(score)
		klog.V(4).Infof("[least-token] Final score pod %s/%s: tokens=%.0f, maxTokens=%.0f, normalizedScore=%d",
			info.Pod.Namespace, info.Pod.Name, tokenCounts[info], maxTokens, int(score))
	}

	return scoreResults
}
