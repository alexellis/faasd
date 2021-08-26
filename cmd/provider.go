package cmd

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"

	"github.com/containerd/containerd"
	bootstrap "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg/cninetwork"
	faasdlogs "github.com/openfaas/faasd/pkg/logs"
	"github.com/openfaas/faasd/pkg/provider/config"
	"github.com/openfaas/faasd/pkg/provider/handlers"
	"github.com/spf13/cobra"
)

func makeProviderCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "provider",
		Short: "Run the faasd-provider",
	}

	command.Flags().String("pull-policy", "Always", `Set to "Always" to force a pull of images upon deployment, or "IfNotPresent" to try to use a cached image.`)
	command.Flags().String("nameserver", "8.8.8.8", `Set to the DNS server you want to use for resolving DNS queries`)

	command.RunE = func(_ *cobra.Command, _ []string) error {

		pullPolicy, flagErr := command.Flags().GetString("pull-policy")
		if flagErr != nil {
			return flagErr
		}

		alwaysPull := false
		if pullPolicy == "Always" {
			alwaysPull = true
		}

		nameserver, flagErr := command.Flags().GetString("nameserver")
		if flagErr != nil {
			return flagErr
		}

		config, providerConfig, err := config.ReadFromEnv(types.OsEnv{})
		if err != nil {
			return err
		}

		log.Printf("faasd-provider starting..\tService Timeout: %s\n", config.WriteTimeout.String())
		printVersion()

		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		writeHostsErr := ioutil.WriteFile(path.Join(wd, "hosts"),
			[]byte(`127.0.0.1	localhost`), workingDirectoryPermission)

		if writeHostsErr != nil {
			return fmt.Errorf("cannot write hosts file: %s", writeHostsErr)
		}

		writeResolvErr := ioutil.WriteFile(path.Join(wd, "resolv.conf"),
			[]byte(fmt.Sprintf("nameserver %s", nameserver)), workingDirectoryPermission)

		if writeResolvErr != nil {
			return fmt.Errorf("cannot write resolv.conf file: %s", writeResolvErr)
		}

		cni, err := cninetwork.InitNetwork()
		if err != nil {
			return err
		}

		client, err := containerd.New(providerConfig.Sock)
		if err != nil {
			return err
		}

		defer client.Close()

		invokeResolver := handlers.NewInvokeResolver(client)

		userSecretPath := path.Join(wd, "secrets")

		bootstrapHandlers := types.FaaSHandlers{
			FunctionProxy:        proxy.NewHandlerFunc(*config, invokeResolver),
			DeleteHandler:        handlers.MakeDeleteHandler(client, cni),
			DeployHandler:        handlers.MakeDeployHandler(client, cni, userSecretPath, alwaysPull),
			FunctionReader:       handlers.MakeReadHandler(client),
			ReplicaReader:        handlers.MakeReplicaReaderHandler(client),
			ReplicaUpdater:       handlers.MakeReplicaUpdateHandler(client, cni),
			UpdateHandler:        handlers.MakeUpdateHandler(client, cni, userSecretPath, alwaysPull),
			HealthHandler:        func(w http.ResponseWriter, r *http.Request) {},
			InfoHandler:          handlers.MakeInfoHandler(Version, GitCommit),
			ListNamespaceHandler: listNamespaces(),
			SecretHandler:        handlers.MakeSecretHandler(client, userSecretPath),
			LogHandler:           logs.NewLogHandlerFunc(faasdlogs.New(), config.ReadTimeout),
		}

		log.Printf("Listening on TCP port: %d\n", *config.TCPPort)
		bootstrap.Serve(&bootstrapHandlers, config)
		return nil
	}

	return command
}

func listNamespaces() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		list := []string{""}
		out, _ := json.Marshal(list)
		w.Write(out)
	}
}
