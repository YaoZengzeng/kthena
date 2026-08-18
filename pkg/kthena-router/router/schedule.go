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

package router

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/kthena/pkg/apis/networking/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
	"github.com/volcano-sh/kthena/pkg/kthena-router/scheduler/framework"
	"github.com/volcano-sh/kthena/pkg/kthena-router/utils"
)

const (
	// SchedulePath selects a serving instance for a request without proxying it.
	SchedulePath = "/v1/schedule"
	// ScheduleReleasePath reports that a previously selected instance finished
	// serving a request.
	ScheduleReleasePath = "/v1/schedule/release"

	rolePrefill = "prefill"
	roleDecode  = "decode"
)

// ScheduleInstance identifies a serving instance selected by the router.
type ScheduleInstance struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Address   string `json:"address,omitempty"`
	Port      int32  `json:"port,omitempty"`
	// Role is set to "prefill" or "decode" for prefill/decode disaggregated
	// model servers and empty otherwise.
	Role string `json:"role,omitempty"`
}

// ScheduleResponse is the body returned by SchedulePath.
type ScheduleResponse struct {
	// Model is the model name the selected instances serve, which the caller
	// has to send upstream instead of the routed model name.
	Model     string             `json:"model"`
	Instances []ScheduleInstance `json:"instances"`
}

// ScheduleReleaseRequest is the body accepted by ScheduleReleasePath.
type ScheduleReleaseRequest struct {
	Instances []ScheduleInstance `json:"instances"`
}

// Schedule selects a serving instance for the request and returns its address
// instead of proxying to it. It lets a caller that dispatches requests itself,
// such as a reinforcement learning rollout engine, still benefit from the
// router's prefix cache affinity and load awareness. Those callers cannot go
// through the proxy path because their own dispatch carries state the serving
// protocol does not expose, such as the weight version a rollout replica
// generated a response with.
//
// The router counts the request as in flight on the selected instance until
// the caller reports completion through Release, so scoring stays accurate for
// concurrent requests.
func (r *Router) Schedule(c *gin.Context) {
	modelRequest, err := ParseModelRequest(c)
	if err != nil {
		return
	}
	modelName := modelRequest["model"].(string)

	prompt, err := utils.ParsePrompt(modelRequest)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, "prompt not found")
		return
	}

	gatewayKey := c.GetString(GatewayKey)
	modelTarget, isLora, _, err := r.store.MatchModelTarget(modelName, c.Request, gatewayKey)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, fmt.Sprintf("can't match model route: %v", err))
		return
	}
	if modelTarget.Kind != datastore.ModelTargetKindModelServer {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			fmt.Sprintf("model %s routes to a %s, which has no schedulable instances", modelName, modelTarget.Kind))
		return
	}

	pods, modelServer, err := r.getPodsAndServer(modelTarget.Name)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, err.Error())
		return
	}
	if len(pods) == 0 {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable,
			fmt.Sprintf("no available pods for model server: %v", modelTarget.Name))
		return
	}

	upstreamModel := modelName
	if modelServer.Spec.Model != nil && !isLora {
		upstreamModel = *modelServer.Spec.Model
	}
	var pdGroup *v1alpha1.PDGroup
	if modelServer.Spec.WorkloadSelector != nil {
		pdGroup = modelServer.Spec.WorkloadSelector.PDGroup
	}

	ctx := &framework.Context{
		Model:           modelName,
		Prompt:          prompt,
		ModelServerName: modelTarget.Name,
		UpstreamModel:   upstreamModel,
		PDGroup:         pdGroup,
	}
	if err := r.scheduler.Schedule(ctx, pods); err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, fmt.Sprintf("can't schedule to target pod: %v", err))
		return
	}

	instances := selectedInstances(ctx, modelServer.Spec.WorkloadPort.Port)
	if len(instances) == 0 {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, "scheduler selected no instance")
		return
	}
	for _, instance := range instances {
		r.store.IncrPodOnFlightRequests(types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name})
	}
	// Record the selection in the prefix cache so that follow-up requests
	// sharing this prompt prefix are steered to the same instances.
	r.scheduler.RunPostHooks(ctx, 0)

	c.JSON(http.StatusOK, ScheduleResponse{Model: upstreamModel, Instances: instances})
}

// Release reports that the instances selected by Schedule finished serving a
// request, so the router stops counting it as in flight.
func (r *Router) Release(c *gin.Context) {
	var request ScheduleReleaseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	for _, instance := range request.Instances {
		if instance.Name == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, "instance name is required")
			return
		}
		r.store.DecrPodOnFlightRequests(types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name})
	}
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// selectedInstances converts the scheduling result into the response instances,
// covering both the aggregated and the prefill/decode disaggregated case.
func selectedInstances(ctx *framework.Context, port int32) []ScheduleInstance {
	if len(ctx.BestPods) > 0 {
		return []ScheduleInstance{newScheduleInstance(ctx.BestPods[0], port, "")}
	}

	var instances []ScheduleInstance
	if len(ctx.PrefillPods) > 0 && ctx.PrefillPods[0] != nil {
		instances = append(instances, newScheduleInstance(ctx.PrefillPods[0], port, rolePrefill))
	}
	if len(ctx.DecodePods) > 0 && ctx.DecodePods[0] != nil {
		instances = append(instances, newScheduleInstance(ctx.DecodePods[0], port, roleDecode))
	}
	return instances
}

func newScheduleInstance(podInfo *datastore.PodInfo, port int32, role string) ScheduleInstance {
	pod := podInfo.GetPod()
	klog.V(4).Infof("scheduled request to %s/%s", pod.Namespace, pod.Name)
	return ScheduleInstance{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		Address:   pod.Status.PodIP,
		Port:      utils.EndpointPort(pod, port),
		Role:      role,
	}
}
