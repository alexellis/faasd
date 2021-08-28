package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/containerd/containerd"
	"github.com/openfaas/faas-provider/types"
)

func MakeReadHandler(client *containerd.Client) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		res := []types.FunctionStatus{}
		fns, err := ListFunctions(client, lookupNamespace)
		if err != nil {
			log.Printf("[Read] error listing functions. Error: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		for _, fn := range fns {
			annotations := &fn.annotations
			labels := &fn.labels
			res = append(res, types.FunctionStatus{
				Name:        fn.name,
				Image:       fn.image,
				Replicas:    uint64(fn.replicas),
				Namespace:   fn.namespace,
				Labels:      labels,
				Annotations: annotations,
				Secrets:     fn.secrets,
				EnvVars:     fn.envVars,
				EnvProcess:  fn.envProcess,
				CreatedAt:   fn.createdAt,
			})
		}

		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
