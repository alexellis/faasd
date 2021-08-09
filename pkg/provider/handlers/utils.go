package handlers

import (
	"net/http"
	"path"

	faasd "github.com/openfaas/faasd/pkg"
)

func getRequestNamespace(namespace string) string {

	if len(namespace) > 0 {
		return namespace
	}
	return faasd.FunctionNamespace
}

func readNamespaceFromQuery(r *http.Request) string {
	q := r.URL.Query()
	return q.Get("namespace")
}

func getNamespaceSecretMountPath(userSecretPath string, namespace string) string {
	return path.Join(userSecretPath, namespace)
}
