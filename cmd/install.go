package cmd

import (
	"fmt"
	"os"
	"path"

	systemd "github.com/alexellis/faasd/pkg/systemd"
	"github.com/pkg/errors"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install faasd",
	RunE:  runInstall,
}

func runInstall(_ *cobra.Command, _ []string) error {

	if basicAuthErr := makeBasicAuthFiles(); basicAuthErr != nil {
		return errors.Wrap(basicAuthErr, "cannot create basic-auth-* files")
	}

	wd := "/run/faasd"
	if err := ensureWorkingDir(wd); err != nil {
		return err
	}

	err := binExists("/usr/local/bin/", "faas-containerd")
	if err != nil {
		return err
	}

	err = binExists("/usr/local/bin/", "faasd")
	if err != nil {
		return err
	}

	err = binExists("/usr/local/bin/", "netns")
	if err != nil {
		return err
	}

	err = systemd.InstallUnit("faas-containerd", wd)
	if err != nil {
		return err
	}

	err = systemd.InstallUnit("faasd", wd)
	if err != nil {
		return err
	}

	err = systemd.DaemonReload()
	if err != nil {
		return err
	}

	err = systemd.Enable("faas-containerd")
	if err != nil {
		return err
	}

	err = systemd.Enable("faasd")
	if err != nil {
		return err
	}

	err = systemd.Start("faas-containerd")
	if err != nil {
		return err
	}

	err = systemd.Start("faasd")
	if err != nil {
		return err
	}

	return nil
}

func binExists(folder, name string) error {
	findPath := path.Join(folder, name)
	if _, err := os.Stat(findPath); err != nil {
		return fmt.Errorf("unable to stat %s, install this binary before continuing", findPath)
	}
	return nil
}

func ensureWorkingDir(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		err = os.MkdirAll("/run/faasd", 0600)
		if err != nil {
			return err
		}
	}

	return nil
}
