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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/kthena/pkg/kthena-router/datastore"
)

const (
	runtimeRegisterPath = "/kvcache/routers/register"

	defaultRuntimePort             = 9000
	defaultRegistrationIntervalSec = 30
	defaultRegistrationTTLSec      = 90
)

// routerRegistrationRequest is the body posted to the runtime sidecar's
// /kvcache/routers/register endpoint. Kept in sync by hand with
// python/kthena/runtime/app.py.
type routerRegistrationRequest struct {
	RouterID   string `json:"router_id"`
	Endpoint   string `json:"endpoint"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// kvRouterRegistrar periodically registers this router instance with the
// runtime sidecar of every known model-serving pod, so the sidecars can push
// KV cache events back. Registration doubles as a heartbeat: sidecars expire
// routers that stop re-registering within the TTL.
type kvRouterRegistrar struct {
	store       datastore.Store
	routerID    string
	endpoint    string // base URL the sidecar pushes events to, e.g. http://10.0.0.5:9080
	runtimePort int
	interval    time.Duration
	ttlSeconds  int
	client      *http.Client
}

func newKVRouterRegistrar(store datastore.Store, routerID, endpoint string,
	runtimePort, intervalSeconds, ttlSeconds int) *kvRouterRegistrar {
	return &kvRouterRegistrar{
		store:       store,
		routerID:    routerID,
		endpoint:    endpoint,
		runtimePort: runtimePort,
		interval:    time.Duration(intervalSeconds) * time.Second,
		ttlSeconds:  ttlSeconds,
		client:      &http.Client{Timeout: 3 * time.Second},
	}
}

// routerSelfEndpoint derives the base URL runtime sidecars should push KV
// events to. POD_IP must be injected via the downward API on the router
// deployment; without it memory mode cannot receive pushes.
func routerSelfEndpoint(eventsPort int) (string, error) {
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		return "", fmt.Errorf("POD_IP environment variable is not set")
	}
	return fmt.Sprintf("http://%s:%d", podIP, eventsPort), nil
}

// routerInstanceID identifies this router replica in sidecar registries.
func routerInstanceID() string {
	if name := os.Getenv("POD_NAME"); name != "" {
		return name
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "kthena-router"
	}
	return hostname
}

// run registers with all known pods immediately and then on every tick until
// ctx is done.
func (r *kvRouterRegistrar) run(ctx context.Context) {
	r.registerAll(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.registerAll(ctx)
		}
	}
}

func (r *kvRouterRegistrar) registerAll(ctx context.Context) {
	pods := r.store.GetAllPods()
	registered := 0
	for _, podInfo := range pods {
		pod := podInfo.GetPod()
		if pod == nil || pod.Status.PodIP == "" || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if err := r.registerWithPod(ctx, pod.Status.PodIP); err != nil {
			// Pods without a runtime sidecar (or not in memory mode) are expected
			// to fail; keep this quiet.
			klog.V(4).Infof("KVCacheAware: registration with pod %s/%s (%s) failed: %v",
				pod.Namespace, pod.Name, pod.Status.PodIP, err)
			continue
		}
		registered++
	}
	klog.V(4).Infof("KVCacheAware: registered with %d/%d runtime sidecars", registered, len(pods))
}

func (r *kvRouterRegistrar) registerWithPod(ctx context.Context, podIP string) error {
	body, err := json.Marshal(routerRegistrationRequest{
		RouterID:   r.routerID,
		Endpoint:   r.endpoint,
		TTLSeconds: r.ttlSeconds,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s:%d%s", podIP, r.runtimePort, runtimeRegisterPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
