package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	gocni "github.com/containerd/go-cni"
	"github.com/docker/distribution/reference"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	cninetwork "github.com/openfaas/faasd/pkg/cninetwork"
	"github.com/openfaas/faasd/pkg/service"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/resource"
)

const annotationLabelPrefix = "com.openfaas.annotations."

// MakeDeployHandler returns a handler to deploy a function
func MakeDeployHandler(client *containerd.Client, cni gocni.CNI, secretMountPath string, alwaysPull bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Deploy] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Deploy] - error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		err = validateSecrets(secretMountPath, req.Secrets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		name := req.Service
		ctx := namespaces.WithNamespace(context.Background(), faasd.FunctionNamespace)

		deployErr := deploy(ctx, req, client, cni, secretMountPath, alwaysPull)
		if deployErr != nil {
			log.Printf("[Deploy] error deploying %s, error: %s\n", name, deployErr)
			http.Error(w, deployErr.Error(), http.StatusBadRequest)
			return
		}
	}
}

// prepull is an optimization which means an image can be pulled before a deployment
// request, since a deployment request first deletes the active function before
// trying to deploy a new one.
func prepull(ctx context.Context, req types.FunctionDeployment, client *containerd.Client, alwaysPull bool) (containerd.Image, error) {
	start := time.Now()
	r, err := reference.ParseNormalizedNamed(req.Image)
	if err != nil {
		return nil, err
	}

	imgRef := reference.TagNameOnly(r).String()

	snapshotter := ""
	if val, ok := os.LookupEnv("snapshotter"); ok {
		snapshotter = val
	}

	image, err := service.PrepareImage(ctx, client, imgRef, snapshotter, alwaysPull)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to pull image %s", imgRef)
	}

	size, _ := image.Size(ctx)
	log.Printf("Image for: %s size: %d, took: %fs\n", image.Name(), size, time.Since(start).Seconds())

	return image, nil
}

func deploy(ctx context.Context, req types.FunctionDeployment, client *containerd.Client, cni gocni.CNI, secretMountPath string, alwaysPull bool) error {

	snapshotter := ""
	if val, ok := os.LookupEnv("snapshotter"); ok {
		snapshotter = val
	}

	image, err := prepull(ctx, req, client, alwaysPull)
	if err != nil {
		return err
	}

	envs := prepareEnv(req.EnvProcess, req.EnvVars)
	mounts := getMounts()

	for _, secret := range req.Secrets {
		mounts = append(mounts, specs.Mount{
			Destination: path.Join("/var/openfaas/secrets", secret),
			Type:        "bind",
			Source:      path.Join(secretMountPath, secret),
			Options:     []string{"rbind", "ro"},
		})
	}

	name := req.Service

	labels, err := buildLabels(&req)
	if err != nil {
		return fmt.Errorf("Unable to apply labels to conatiner: %s, error: %s", name, err)
	}

	var memory *specs.LinuxMemory
	if req.Limits != nil && len(req.Limits.Memory) > 0 {
		memory = &specs.LinuxMemory{}

		qty, err := resource.ParseQuantity(req.Limits.Memory)
		if err != nil {
			log.Printf("error parsing (%q) as quantity: %s", req.Limits.Memory, err.Error())
		}
		v := qty.Value()
		memory.Limit = &v
	}

	container, err := client.NewContainer(
		ctx,
		name,
		containerd.WithImage(image),
		containerd.WithSnapshotter(snapshotter),
		containerd.WithNewSnapshot(name+"-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image),
			oci.WithHostname(name),
			oci.WithCapabilities([]string{"CAP_NET_RAW"}),
			oci.WithMounts(mounts),
			oci.WithEnv(envs),
			withMemory(memory)),
		containerd.WithContainerLabels(labels),
	)

	if err != nil {
		return fmt.Errorf("unable to create container: %s, error: %s", name, err)
	}

	return createTask(ctx, client, container, cni)

}

func buildLabels(request *types.FunctionDeployment) (map[string]string, error) {
	// Adapted from faas-swarm/handlers/deploy.go:buildLabels
	labels := map[string]string{}

	if request.Labels != nil {
		for k, v := range *request.Labels {
			labels[k] = v
		}
	}

	if request.Annotations != nil {
		for k, v := range *request.Annotations {
			key := fmt.Sprintf("%s%s", annotationLabelPrefix, k)
			if _, ok := labels[key]; !ok {
				labels[key] = v
			} else {
				return nil, errors.New(fmt.Sprintf("Key %s cannot be used as a label due to a conflict with annotation prefix %s", k, annotationLabelPrefix))
			}
		}
	}

	return labels, nil
}

func createTask(ctx context.Context, client *containerd.Client, container containerd.Container, cni gocni.CNI) error {

	name := container.ID()

	task, taskErr := container.NewTask(ctx, cio.BinaryIO("/usr/local/bin/faasd", nil))

	if taskErr != nil {
		return fmt.Errorf("unable to start task: %s, error: %s", name, taskErr)
	}

	log.Printf("Container ID: %s\tTask ID %s:\tTask PID: %d\t\n", name, task.ID(), task.Pid())

	labels := map[string]string{}
	_, err := cninetwork.CreateCNINetwork(ctx, cni, task, labels)

	if err != nil {
		return err
	}

	ip, err := cninetwork.GetIPAddress(name, task.Pid())
	if err != nil {
		return err
	}

	log.Printf("%s has IP: %s.\n", name, ip)

	_, waitErr := task.Wait(ctx)
	if waitErr != nil {
		return errors.Wrapf(waitErr, "Unable to wait for task to start: %s", name)
	}

	if startErr := task.Start(ctx); startErr != nil {
		return errors.Wrapf(startErr, "Unable to start task: %s", name)
	}
	return nil
}

func prepareEnv(envProcess string, reqEnvVars map[string]string) []string {
	envs := []string{}
	fprocessFound := false
	fprocess := "fprocess=" + envProcess
	if len(envProcess) > 0 {
		fprocessFound = true
	}

	for k, v := range reqEnvVars {
		if k == "fprocess" {
			fprocessFound = true
			fprocess = v
		} else {
			envs = append(envs, k+"="+v)
		}
	}
	if fprocessFound {
		envs = append(envs, fprocess)
	}
	return envs
}

func getMounts() []specs.Mount {
	wd, _ := os.LookupEnv("hosts_dir")
	if len(wd) == 0 {
		wd = "/var/lib/faasd"
	}

	mounts := []specs.Mount{}
	mounts = append(mounts, specs.Mount{
		Destination: "/etc/resolv.conf",
		Type:        "bind",
		Source:      path.Join(wd, "resolv.conf"),
		Options:     []string{"rbind", "ro"},
	})

	mounts = append(mounts, specs.Mount{
		Destination: "/etc/hosts",
		Type:        "bind",
		Source:      path.Join(wd, "hosts"),
		Options:     []string{"rbind", "ro"},
	})
	return mounts
}

func validateSecrets(secretMountPath string, secrets []string) error {
	for _, secret := range secrets {
		if _, err := os.Stat(path.Join(secretMountPath, secret)); err != nil {
			return fmt.Errorf("unable to find secret: %s", secret)
		}
	}
	return nil
}

func withMemory(mem *specs.LinuxMemory) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *oci.Spec) error {
		if mem != nil {
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			if s.Linux.Resources == nil {
				s.Linux.Resources = &specs.LinuxResources{}
			}
			if s.Linux.Resources.Memory == nil {
				s.Linux.Resources.Memory = &specs.LinuxMemory{}
			}
			s.Linux.Resources.Memory.Limit = mem.Limit
		}
		return nil
	}
}
