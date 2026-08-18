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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"istio.io/istio/pkg/util/sets"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/networking/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
)

// setupPickerTestRouter registers a ModelServer backed by two instances and a
// ModelRoute pointing at it, so the picker has something to select from.
func setupPickerTestRouter(t *testing.T) (*Router, datastore.Store, *httptest.Server) {
	t.Helper()

	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	router, store, backend := setupTestRouter(t, backendHandler)
	t.Cleanup(backend.Close)

	backendURL, err := url.Parse(backend.URL)
	assert.NoError(t, err)
	backendPort, err := strconv.Atoi(backendURL.Port())
	assert.NoError(t, err)

	modelServer := &aiv1alpha1.ModelServer{
		ObjectMeta: v1.ObjectMeta{Name: "ms-1", Namespace: "default"},
		Spec: aiv1alpha1.ModelServerSpec{
			Model:           func(s string) *string { return &s }("upstream-model"),
			WorkloadPort:    aiv1alpha1.WorkloadPort{Port: int32(backendPort)},
			InferenceEngine: "vLLM",
		},
	}
	podNames := sets.New[types.NamespacedName]()
	store.AddOrUpdateModelServer(modelServer, podNames)
	for _, name := range []string{"pod-1", "pod-2"} {
		podNames.Insert(types.NamespacedName{Name: name, Namespace: "default"})
		pod := &corev1.Pod{
			ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "default"},
			Status:     corev1.PodStatus{PodIP: backendURL.Hostname(), Phase: corev1.PodRunning},
		}
		assert.NoError(t, store.AddOrUpdatePod(pod, []*aiv1alpha1.ModelServer{modelServer}))
	}
	store.AddOrUpdateModelServer(modelServer, podNames)
	assert.NoError(t, store.AddOrUpdateModelRoute(&aiv1alpha1.ModelRoute{
		ObjectMeta: v1.ObjectMeta{Name: "mr-1", Namespace: "default"},
		Spec: aiv1alpha1.ModelRouteSpec{
			ModelName: "routed-model",
			Rules: []*aiv1alpha1.Rule{
				{TargetModels: []*aiv1alpha1.TargetModel{{ModelServerName: "ms-1"}}},
			},
		},
	}))

	return router, store, backend
}

func postJSON(t *testing.T, router *Router, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	request, err := http.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	assert.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	c.Request = request

	router.HandlerFunc()(c)
	return w
}

func TestRouter_Schedule(t *testing.T) {
	prev := EnableEndpointPicker
	EnableEndpointPicker = true
	defer func() { EnableEndpointPicker = prev }()

	tests := []struct {
		name         string
		body         string
		expectedCode int
	}{
		{
			name:         "token id prompt",
			body:         `{"model": "routed-model", "prompt": [1, 2, 3]}`,
			expectedCode: http.StatusOK,
		},
		{
			name:         "text prompt",
			body:         `{"model": "routed-model", "prompt": "hello"}`,
			expectedCode: http.StatusOK,
		},
		{
			name:         "chat prompt",
			body:         `{"model": "routed-model", "messages": [{"role": "user", "content": "hi"}]}`,
			expectedCode: http.StatusOK,
		},
		{
			name:         "unknown model",
			body:         `{"model": "other-model", "prompt": [1]}`,
			expectedCode: http.StatusNotFound,
		},
		{
			name:         "missing prompt",
			body:         `{"model": "routed-model"}`,
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, _ := setupPickerTestRouter(t)

			w := postJSON(t, router, SchedulePath, tt.body)

			assert.Equal(t, tt.expectedCode, w.Code)
			if tt.expectedCode != http.StatusOK {
				return
			}
			var response ScheduleResponse
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, "upstream-model", response.Model)
			assert.Len(t, response.Instances, 1)
			assert.Equal(t, "default", response.Instances[0].Namespace)
			assert.Contains(t, []string{"pod-1", "pod-2"}, response.Instances[0].Name)
			assert.NotEmpty(t, response.Instances[0].Address)
			assert.NotZero(t, response.Instances[0].Port)
		})
	}
}

func TestRouter_ScheduleDisabled(t *testing.T) {
	prev := EnableEndpointPicker
	EnableEndpointPicker = false
	defer func() { EnableEndpointPicker = prev }()

	router, _, _ := setupPickerTestRouter(t)

	// With the picker disabled the path is treated as a regular inference
	// request, which the mock backend answers with an empty 200 body.
	w := postJSON(t, router, SchedulePath, `{"model": "routed-model", "prompt": [1, 2, 3]}`)

	var response ScheduleResponse
	assert.Error(t, json.Unmarshal(w.Body.Bytes(), &response))
}

// TestRouter_ScheduleTracksOnFlightRequests verifies that a scheduled request
// counts as in flight until it is released, which is what keeps load-aware
// scoring correct while the caller dispatches the request itself.
func TestRouter_ScheduleTracksOnFlightRequests(t *testing.T) {
	prev := EnableEndpointPicker
	EnableEndpointPicker = true
	defer func() { EnableEndpointPicker = prev }()

	router, store, _ := setupPickerTestRouter(t)

	w := postJSON(t, router, SchedulePath, `{"model": "routed-model", "prompt": [1, 2, 3]}`)
	assert.Equal(t, http.StatusOK, w.Code)

	var response ScheduleResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	selected := types.NamespacedName{
		Namespace: response.Instances[0].Namespace,
		Name:      response.Instances[0].Name,
	}
	assert.Equal(t, 1, onFlightRequests(t, store, selected))

	release, err := json.Marshal(ScheduleReleaseRequest{Instances: response.Instances})
	assert.NoError(t, err)
	w = postJSON(t, router, ScheduleReleasePath, string(release))

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, 0, onFlightRequests(t, store, selected))
}

func TestRouter_Release(t *testing.T) {
	prev := EnableEndpointPicker
	EnableEndpointPicker = true
	defer func() { EnableEndpointPicker = prev }()

	tests := []struct {
		name         string
		body         string
		expectedCode int
	}{
		{
			name:         "unknown instance is ignored",
			body:         `{"instances": [{"namespace": "default", "name": "pod-404"}]}`,
			expectedCode: http.StatusNoContent,
		},
		{
			name:         "missing instance name",
			body:         `{"instances": [{"namespace": "default"}]}`,
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "malformed body",
			body:         `not json`,
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _, _ := setupPickerTestRouter(t)

			w := postJSON(t, router, ScheduleReleasePath, tt.body)

			assert.Equal(t, tt.expectedCode, w.Code)
		})
	}
}

func onFlightRequests(t *testing.T, store datastore.Store, name types.NamespacedName) int {
	t.Helper()

	pod := store.GetPodInfo(name)
	assert.NotNil(t, pod)
	return int(pod.GetOnFlightRequestNum())
}
