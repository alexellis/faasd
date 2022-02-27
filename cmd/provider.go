package cmd

import (
	"fmt"
	"io"
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
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/cninetwork"
	faasdlogs "github.com/openfaas/faasd/pkg/logs"
	"github.com/openfaas/faasd/pkg/provider/config"
	"github.com/openfaas/faasd/pkg/provider/handlers"
	"github.com/spf13/cobra"
)

const secretDirPermission = 0755

func makeProviderCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "provider",
		Short: "Run the faasd-provider",
	}

	command.Flags().String("pull-policy", "Always", `Set to "Always" to force a pull of images upon deployment, or "IfNotPresent" to try to use a cached image.`)

	command.RunE = func(_ *cobra.Command, _ []string) error {

		pullPolicy, flagErr := command.Flags().GetString("pull-policy")
		if flagErr != nil {
			return flagErr
		}

		alwaysPull := false
		if pullPolicy == "Always" {
			alwaysPull = true
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
			[]byte(`nameserver 8.8.8.8`), workingDirectoryPermission)

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

		baseUserSecretsPath := path.Join(wd, "secrets")
		if err := moveSecretsToDefaultNamespaceSecrets(
			baseUserSecretsPath,
			faasd.DefaultFunctionNamespace); err != nil {
			return err
		}

		bootstrapHandlers := types.FaaSHandlers{
			FunctionProxy:        proxy.NewHandlerFunc(*config, invokeResolver),
			DeleteHandler:        handlers.MakeDeleteHandler(client, cni),
			DeployHandler:        handlers.MakeDeployHandler(client, cni, baseUserSecretsPath, alwaysPull),
			FunctionReader:       handlers.MakeReadHandler(client),
			ReplicaReader:        handlers.MakeReplicaReaderHandler(client),
			ReplicaUpdater:       handlers.MakeReplicaUpdateHandler(client, cni, invokeResolver),
			UpdateHandler:        handlers.MakeUpdateHandler(client, cni, baseUserSecretsPath, alwaysPull),
			HealthHandler:        func(w http.ResponseWriter, r *http.Request) {},
			InfoHandler:          handlers.MakeInfoHandler(Version, GitCommit),
			ListNamespaceHandler: handlers.MakeNamespacesLister(client),
			SecretHandler:        handlers.MakeSecretHandler(client.NamespaceService(), baseUserSecretsPath),
			LogHandler:           logs.NewLogHandlerFunc(faasdlogs.New(), config.ReadTimeout),
		}

		log.Printf("Listening on TCP port: %d\n", *config.TCPPort)
		bootstrap.Serve(&bootstrapHandlers, config)
		return nil
	}

	return command
}

/*
* Mutiple namespace support was added after release 0.13.0
* Function will help users to migrate on multiple namespace support of faasd
 */
func moveSecretsToDefaultNamespaceSecrets(baseSecretPath string, defaultNamespace string) error {
	newSecretPath := path.Join(baseSecretPath, defaultNamespace)

	err := ensureSecretsDir(newSecretPath)
	if err != nil {
		return err
	}

	files, err := ioutil.ReadDir(baseSecretPath)
	if err != nil {
		return err
	}

	for _, f := range files {
		if !f.IsDir() {

			newPath := path.Join(newSecretPath, f.Name())

			// A non-nil error means the file wasn't found in the
			// destination path
			if _, err := os.Stat(newPath); err != nil {
				oldPath := path.Join(baseSecretPath, f.Name())

				if err := copyFile(oldPath, newPath); err != nil {
					return err
				}

				log.Printf("[Migration] Copied %s to %s", oldPath, newPath)
			}
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	inputFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s failed %w", src, err)
	}
	defer inputFile.Close()

	outputFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_APPEND, secretDirPermission)
	if err != nil {
		return fmt.Errorf("opening %s failed %w", dst, err)
	}
	defer outputFile.Close()

	// Changed from os.Rename due to issue in #201
	if _, err := io.Copy(outputFile, inputFile); err != nil {
		return fmt.Errorf("writing into %s failed %w", outputFile.Name(), err)
	}

	return nil
}
