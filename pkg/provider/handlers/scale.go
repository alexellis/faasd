package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"

	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
)

func MakeReplicaUpdateHandler(client *containerd.Client, cni gocni.CNI, resolver proxy.BaseURLResolver) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Scale] request: %s\n", string(body))

		req := types.ScaleServiceRequest{}
		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("[Scale] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		namespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(client.NamespaceService(), namespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		name := req.ServiceName

		fn, err := GetFunction(client, name, namespace)
		if err != nil {
			msg := fmt.Sprintf("service %s not found", name)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		healthPath := "/_/healthz"
		if v := fn.annotations["com.openfaas.health.http.path"]; len(v) > 0 {
			healthPath = v
		}

		ctx := namespaces.WithNamespace(context.Background(), namespace)
		ctr, err := client.LoadContainer(ctx, name)
		if err != nil {
			msg := fmt.Sprintf("cannot load service %s, error: %s", name, err)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		var taskExists bool
		var taskStatus *containerd.Status

		task, err := ctr.Task(ctx, nil)
		if err != nil {
			msg := fmt.Sprintf("cannot load task for service %s, error: %s", name, err)
			log.Printf("[Scale] %s\n", msg)
			taskExists = false
		} else {
			taskExists = true
			status, err := task.Status(ctx)
			if err != nil {
				msg := fmt.Sprintf("cannot load task status for %s, error: %s", name, err)
				log.Printf("[Scale] %s\n", msg)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			} else {
				taskStatus = &status
			}
		}

		createNewTask := false

		// Scale to zero
		if req.Replicas == 0 {
			// If a task is running, pause it
			if taskExists && taskStatus.Status == containerd.Running {
				if err := task.Pause(ctx); err != nil {
					werr := fmt.Errorf("error pausing task %s, error: %s", name, err)
					log.Printf("[Scale] %s\n", werr.Error())
					http.Error(w, werr.Error(), http.StatusNotFound)
					return
				}
			}

			// Otherwise, no action is required
			return
		}

		if taskExists {
			if taskStatus != nil {
				if taskStatus.Status == containerd.Paused {
					if err := task.Resume(ctx); err != nil {
						log.Printf("[Scale] error resuming task %s, error: %s\n", name, err)
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
				} else if taskStatus.Status == containerd.Stopped {
					// Stopped tasks cannot be restarted, must be removed, and created again
					if _, err := task.Delete(ctx); err != nil {
						log.Printf("[Scale] error deleting stopped task %s, error: %s\n", name, err)
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					createNewTask = true
				}
			}
		} else {
			createNewTask = true
		}

		if createNewTask {
			err := createTask(ctx, client, ctr, cni)
			if err != nil {
				log.Printf("[Scale] error deploying %s, error: %s\n", name, err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		if err := waitUntilHealthy(name, resolver, healthPath); err != nil {
			log.Printf("[Scale] error waiting for function %s to become ready, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
}

// waitUntilHealthy blocks until the healthPath returns a HTTP 200 for the
// IP address resolved for the given function.
// Maximum retries: 100
// Delay between each attempt: 20ms
// A custom path can be set via an annotation in the function's spec:
//  com.openfaas.health.http.path: /handlers/ready
//
func waitUntilHealthy(name string, resolver proxy.BaseURLResolver, healthPath string) error {
	endpoint, err := resolver.Resolve(name)
	if err != nil {
		return err
	}

	host, port, _ := net.SplitHostPort(endpoint.Host)
	u, err := url.Parse(fmt.Sprintf("http://%s:%s%s", host, port, healthPath))
	if err != nil {
		return err
	}

	// Try to hit the health endpoint and block until
	// ready.
	attempts := 100
	pause := time.Millisecond * 20
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		if res.Body != nil {
			res.Body.Close()
		}

		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected health status: %d", res.StatusCode)
		}

		if err == nil {
			break
		}

		time.Sleep(pause)
	}

	return nil
}
