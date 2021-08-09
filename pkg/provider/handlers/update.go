package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	"github.com/openfaas/faas-provider/types"

	"github.com/openfaas/faasd/pkg/cninetwork"
	"github.com/openfaas/faasd/pkg/service"
)

func MakeUpdateHandler(client *containerd.Client, cni gocni.CNI, secretMountPath string, alwaysPull bool) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Update] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Update] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		name := req.Service
		namespace := getRequestNamespace(req.Namespace)
		namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, namespace)

		function, err := GetFunction(client, name, namespace)
		if err != nil {
			msg := fmt.Sprintf("service %s not found", name)
			log.Printf("[Update] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		err = validateSecrets(namespaceSecretMountPath, req.Secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		ctx := namespaces.WithNamespace(context.Background(), namespace)

		if _, err := prepull(ctx, req, client, alwaysPull); err != nil {
			log.Printf("[Update] error with pre-pull: %s, %s\n", name, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		if function.replicas != 0 {
			err = cninetwork.DeleteCNINetwork(ctx, cni, client, name)
			if err != nil {
				log.Printf("[Update] error removing CNI network for %s, %s\n", name, err)
			}
		}

		if err := service.Remove(ctx, client, name); err != nil {
			log.Printf("[Update] error removing %s, %s\n", name, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// The pull has already been done in prepull, so we can force this pull to "false"
		pull := false

		if err := deploy(ctx, req, client, cni, namespaceSecretMountPath, pull); err != nil {
			log.Printf("[Update] error deploying %s, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
}
