package pkg

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"sort"

	"github.com/alexellis/k3sup/pkg/env"
	"github.com/compose-spec/compose-go/loader"
	compose "github.com/compose-spec/compose-go/types"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	gocni "github.com/containerd/go-cni"
	"github.com/openfaas/faasd/pkg/cninetwork"
	"github.com/openfaas/faasd/pkg/service"
	"github.com/pkg/errors"

	"github.com/containerd/containerd/namespaces"
	"github.com/opencontainers/runtime-spec/specs-go"
)

const (
	workingDirectoryPermission = 0644
)

type Service struct {
	// Image is the container image registry reference, in an OCI format.
	Image     string
	Env       []string
	Name      string
	Mounts    []Mount
	Caps      []string
	Args      []string
	DependsOn []string
	Ports     []ServicePort

	// User in the docker-compose.yaml spec can set as follows:
	// a user-id, username, userid:groupid or user:group
	User string
}

type ServicePort struct {
	TargetPort uint32
	Port       uint32
	HostIP     string
}

type Mount struct {
	Src  string
	Dest string
}

type Supervisor struct {
	client *containerd.Client
	cni    gocni.CNI
}

func NewSupervisor(sock string) (*Supervisor, error) {
	client, err := containerd.New(sock)
	if err != nil {
		return nil, err
	}

	cni, err := cninetwork.InitNetwork()
	if err != nil {
		return nil, err
	}

	return &Supervisor{
		client: client,
		cni:    cni,
	}, nil
}

func (s *Supervisor) Start(svcs []Service) error {
	ctx := namespaces.WithNamespace(context.Background(), FaasdNamespace)

	wd, _ := os.Getwd()

	gw, err := cninetwork.CNIGateway()
	if err != nil {
		return err
	}
	hosts := fmt.Sprintf(`
127.0.0.1	localhost
%s	faasd-provider`, gw)

	writeHostsErr := ioutil.WriteFile(path.Join(wd, "hosts"),
		[]byte(hosts), workingDirectoryPermission)

	if writeHostsErr != nil {
		return fmt.Errorf("cannot write hosts file: %s", writeHostsErr)
	}

	images := map[string]containerd.Image{}

	for _, svc := range svcs {
		fmt.Printf("Preparing %s with image: %s\n", svc.Name, svc.Image)

		img, err := service.PrepareImage(ctx, s.client, svc.Image, defaultSnapshotter, faasServicesPullAlways)
		if err != nil {
			return err
		}
		images[svc.Name] = img
		size, _ := img.Size(ctx)
		fmt.Printf("Prepare done for: %s, %d bytes\n", svc.Image, size)
	}

	for _, svc := range svcs {
		fmt.Printf("Removing old container for: %s\n", svc.Name)
		containerErr := service.Remove(ctx, s.client, svc.Name)
		if containerErr != nil {
			return containerErr
		}
	}

	order := buildDeploymentOrder(svcs)

	for _, key := range order {

		var svc *Service
		for _, s := range svcs {
			if s.Name == key {
				svc = &s
				break
			}
		}

		fmt.Printf("Starting: %s\n", svc.Name)

		image := images[svc.Name]

		mounts := []specs.Mount{}
		if len(svc.Mounts) > 0 {
			for _, mnt := range svc.Mounts {
				mounts = append(mounts, specs.Mount{
					Source:      mnt.Src,
					Destination: mnt.Dest,
					Type:        "bind",
					Options:     []string{"rbind", "rw"},
				})
			}
		}

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

		if len(svc.User) > 0 {
			log.Printf("Running %s with user: %q", svc.Name, svc.User)
		}

		newContainer, err := s.client.NewContainer(
			ctx,
			svc.Name,
			containerd.WithImage(image),
			containerd.WithNewSnapshot(svc.Name+"-snapshot", image),
			containerd.WithNewSpec(oci.WithImageConfig(image),
				oci.WithHostname(svc.Name),
				withUserOrDefault(svc.User),
				oci.WithCapabilities(svc.Caps),
				oci.WithMounts(mounts),
				withOCIArgs(svc.Args),
				oci.WithEnv(svc.Env)),
		)

		if err != nil {
			log.Printf("Error creating container: %s\n", err)
			return err
		}

		log.Printf("Created container: %s\n", newContainer.ID())

		task, err := newContainer.NewTask(ctx, cio.BinaryIO("/usr/local/bin/faasd", nil))
		if err != nil {
			log.Printf("Error creating task: %s\n", err)
			return err
		}

		labels := map[string]string{}
		_, err = cninetwork.CreateCNINetwork(ctx, s.cni, task, labels)
		if err != nil {
			log.Printf("Error creating CNI for %s: %s", svc.Name, err)
			return err
		}

		ip, err := cninetwork.GetIPAddress(svc.Name, task.Pid())
		if err != nil {
			log.Printf("Error getting IP for %s: %s", svc.Name, err)
			return err
		}

		log.Printf("%s has IP: %s\n", newContainer.ID(), ip)

		hosts, err := ioutil.ReadFile("hosts")
		if err != nil {
			log.Printf("Unable to read hosts file: %s\n", err.Error())
		}

		hosts = []byte(string(hosts) + fmt.Sprintf(`
%s	%s
`, ip, svc.Name))

		if err := ioutil.WriteFile("hosts", hosts, workingDirectoryPermission); err != nil {
			log.Printf("Error writing file: %s %s\n", "hosts", err)
		}

		if _, err := task.Wait(ctx); err != nil {
			log.Printf("Task wait error: %s\n", err)
			return err
		}

		log.Printf("Task: %s\tContainer: %s\n", task.ID(), newContainer.ID())
		// log.Println("Exited: ", exitStatusC)

		if err = task.Start(ctx); err != nil {
			log.Printf("Task start error: %s\n", err)
			return err
		}
	}

	return nil
}

func (s *Supervisor) Close() {
	defer s.client.Close()
}

func (s *Supervisor) Remove(svcs []Service) error {
	ctx := namespaces.WithNamespace(context.Background(), FaasdNamespace)

	for _, svc := range svcs {
		err := cninetwork.DeleteCNINetwork(ctx, s.cni, s.client, svc.Name)
		if err != nil {
			log.Printf("[Delete] error removing CNI network for %s, %s\n", svc.Name, err)
			return err
		}

		err = service.Remove(ctx, s.client, svc.Name)
		if err != nil {
			return err
		}
	}
	return nil
}

func withUserOrDefault(userstr string) oci.SpecOpts {
	if len(userstr) > 0 {
		return oci.WithUser(userstr)
	}

	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		return nil
	}
}

func withOCIArgs(args []string) oci.SpecOpts {
	if len(args) > 0 {
		return oci.WithProcessArgs(args...)
	}

	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		return nil
	}
}

// ParseCompose converts a docker-compose Config into a service list that we can
// pass to the supervisor client Start.
//
// The only anticipated error is a failure if the value mounts are not of type 	`bind`.
func ParseCompose(config *compose.Config) ([]Service, error) {
	services := make([]Service, len(config.Services))
	for idx, s := range config.Services {
		// environment is a map[string]*string
		// but we want a []string

		var env []string

		envKeys := sortedEnvKeys(s.Environment)
		for _, name := range envKeys {
			value := s.Environment[name]
			if value == nil {
				env = append(env, fmt.Sprintf(`%s=""`, name))
			} else {
				env = append(env, fmt.Sprintf(`%s=%s`, name, *value))
			}
		}

		var mounts []Mount
		for _, v := range s.Volumes {
			if v.Type != "bind" {
				return nil, errors.Errorf("unsupported volume mount type '%s' when parsing service '%s'", v.Type, s.Name)
			}
			mounts = append(mounts, Mount{
				Src:  v.Source,
				Dest: v.Target,
			})
		}

		services[idx] = Service{
			Name:  s.Name,
			Image: s.Image,
			// ShellCommand is just an alias of string slice
			Args:      []string(s.Command),
			Caps:      s.CapAdd,
			Env:       env,
			Mounts:    mounts,
			DependsOn: s.DependsOn,
			User:      s.User,
			Ports:     convertPorts(s.Ports),
		}
	}

	return services, nil
}

func convertPorts(ports []compose.ServicePortConfig) []ServicePort {
	servicePorts := []ServicePort{}
	for _, p := range ports {
		servicePorts = append(servicePorts, ServicePort{
			Port:       p.Published,
			TargetPort: p.Target,
			HostIP:     p.HostIP,
		})
	}

	return servicePorts
}

// LoadComposeFile is a helper method for loading a docker-compose file
func LoadComposeFile(wd string, file string) (*compose.Config, error) {
	return LoadComposeFileWithArch(wd, file, env.GetClientArch)
}

// LoadComposeFileWithArch is a helper method for loading a docker-compose file
func LoadComposeFileWithArch(wd string, file string, archGetter ArchGetter) (*compose.Config, error) {

	file = path.Join(wd, file)
	b, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	config, err := loader.ParseYAML(b)
	if err != nil {
		return nil, err
	}

	archSuffix, err := GetArchSuffix(archGetter)
	if err != nil {
		return nil, err
	}

	var files []compose.ConfigFile
	files = append(files, compose.ConfigFile{Filename: file, Config: config})

	return loader.Load(compose.ConfigDetails{
		WorkingDir:  wd,
		ConfigFiles: files,
		Environment: map[string]string{
			"ARCH_SUFFIX": archSuffix,
		},
	})
}

func sortedEnvKeys(env map[string]*string) (keys []string) {
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ArchGetter provides client CPU architecture and
// client OS
type ArchGetter func() (string, string)

// GetArchSuffix provides client CPU architecture and
// client OS from ArchGetter
func GetArchSuffix(getClientArch ArchGetter) (suffix string, err error) {
	clientArch, clientOS := getClientArch()

	if clientOS != "Linux" {
		return "", fmt.Errorf("you can only use faasd with Linux")
	}

	switch clientArch {
	case "x86_64":
		// no suffix needed
		return "", nil
	case "armhf", "armv7l":
		return "-armhf", nil
	case "arm64", "aarch64":
		return "-arm64", nil
	default:
		// unknown, so use the default without suffix for now
		return "", nil
	}
}
