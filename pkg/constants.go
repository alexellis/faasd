package pkg

const (
	// FunctionNamespace is the default containerd namespace functions are created
	FunctionNamespace = "openfaas-fn"

	// FaasdNamespace is the containerd namespace services are created
	FaasdNamespace = "openfaas"

	faasServicesPullAlways = false

	defaultSnapshotter = "overlayfs"
)
