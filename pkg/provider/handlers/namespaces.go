package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/containerd/containerd"
)

func MakeNamespacesLister(client *containerd.Client) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		list := ListNamespaces(client)
		body, _ := json.Marshal(list)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
