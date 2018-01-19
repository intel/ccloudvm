//
// Copyright (c) 2016 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package ccvm

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

type mounts []mount

func (m *mounts) String() string {
	return fmt.Sprint(*m)
}

func (m *mounts) Set(value string) error {
	components := strings.Split(value, ",")
	if len(components) != 3 {
		return fmt.Errorf("--mount parameter should be of format tag,security_model,path")
	}
	*m = append(*m, mount{
		Tag:           components[0],
		SecurityModel: components[1],
		Path:          components[2],
	})
	return nil
}

type ports []portMapping

func (p *ports) String() string {
	return fmt.Sprint(*p)
}

func (p *ports) Set(value string) error {
	components := strings.Split(value, "-")
	if len(components) != 2 {
		return fmt.Errorf("--port parameter should be of format host-guest")
	}
	host, err := strconv.Atoi(components[0])
	if err != nil {
		return fmt.Errorf("host port must be a number")
	}
	guest, err := strconv.Atoi(components[1])
	if err != nil {
		return fmt.Errorf("guest port must be a number")
	}
	*p = append(*p, portMapping{
		Host:  host,
		Guest: guest,
	})
	return nil
}

type drives []drive

func (d *drives) String() string {
	return fmt.Sprint(*d)
}

func (d *drives) Set(value string) error {
	components := strings.Split(value, ",")
	if len(components) < 2 {
		return fmt.Errorf("--drive parameter should be of format path,format[,option]*")
	}
	_, err := os.Stat(components[0])
	if err != nil {
		return errors.Wrapf(err, "Unable to access %s", components[1])
	}
	*d = append(*d, drive{
		Path:    components[0],
		Format:  components[1],
		Options: strings.Join(components[2:], ","),
	})
	return nil
}

// VMFlags provides common flags for customising a workload
func VMFlags(fs *flag.FlagSet, customSpec *VMSpec) {
	fs.IntVar(&customSpec.MemMiB, "mem", customSpec.MemMiB, "Mebibytes of RAM allocated to VM")
	fs.IntVar(&customSpec.CPUs, "cpus", customSpec.CPUs, "VCPUs assigned to VM")
	fs.Var(&customSpec.Mounts, "mount", "directory to mount in guest VM via 9p. Format is tag,security_model,path")
	fs.Var(&customSpec.Drives, "drive", "Host accessible resource to appear as block device in guest VM.  Format is path,format[,option]*")
	fs.Var(&customSpec.PortMappings, "port", "port mapping. Format is host_port-guest_port, e.g., -port 10022-22")
	fs.UintVar(&customSpec.Qemuport, "qemuport", customSpec.Qemuport, "Port to follow qemu logs of the guest machine, eg., --qemuport=9999")
}

func checkDirectory(dir string) error {
	if dir == "" {
		return nil
	}

	if !path.IsAbs(dir) {
		return fmt.Errorf("%s is not an absolute path", dir)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		return errors.Wrapf(err, "Unable to stat %s", dir)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	return nil
}

func prepareCreate(ctx context.Context, workloadName string, debug bool, update bool, customSpec *VMSpec) (*workload, *workspace, error) {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return nil, nil, err
	}

	wkld, err := createWorkload(ctx, ws, workloadName)
	if err != nil {
		return nil, nil, err
	}

	in := &wkld.spec.VM

	err = in.mergeCustom(customSpec)
	if err != nil {
		return nil, nil, err
	}

	ws.Mounts = in.Mounts
	ws.Hostname = wkld.spec.Hostname
	if ws.NoProxy != "" {
		ws.NoProxy = fmt.Sprintf("%s,%s,10.0.2.2", ws.Hostname, ws.NoProxy)
	} else if ws.HTTPProxy != "" || ws.HTTPSProxy != "" {
		ws.NoProxy = fmt.Sprintf("%s,10.0.2.2", ws.Hostname)
	}

	ws.PackageUpgrade = "false"
	if update {
		ws.PackageUpgrade = "true"
	}

	if wkld.spec.NeedsNestedVM && !hostSupportsNestedKVM() {
		return nil, nil, fmt.Errorf("nested KVM is not enabled.  Please enable and try again")
	}

	return wkld, ws, nil
}

// Create sets up the VM
func Create(ctx context.Context, workloadName string, debug bool, update bool, customSpec *VMSpec) error {
	var err error

	wkld, ws, err := prepareCreate(ctx, workloadName, debug, update, customSpec)
	if err != nil {
		return err
	}

	_, err = os.Stat(ws.instanceDir)
	if err == nil {
		return fmt.Errorf("instance already exists")
	}

	fmt.Println("Installing host dependencies")
	installDeps(ctx)

	err = os.MkdirAll(ws.instanceDir, 0755)
	if err != nil {
		return errors.Wrap(err, "unable to create cache dir")
	}

	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws.instanceDir)
		}
	}()

	err = wkld.save(ws)
	if err != nil {
		return errors.Wrap(err, "Unable to save instance state")
	}

	err = prepareSSHKeys(ctx, ws)
	if err != nil {
		return err
	}

	fmt.Printf("Downloading %s\n", wkld.spec.BaseImageName)
	qcowPath, err := downloadFile(ctx, wkld.spec.BaseImageURL, ws.ccvmDir, downloadProgress)
	if err != nil {
		return err
	}

	err = buildISOImage(ctx, ws.instanceDir, wkld.userData, ws, debug)
	if err != nil {
		return err
	}

	spec := wkld.spec.VM
	err = createRootfs(ctx, qcowPath, ws.instanceDir, spec.DiskGiB)
	if err != nil {
		return err
	}

	fmt.Printf("Booting VM with %d MiB RAM and %d cpus\n", spec.MemMiB, spec.CPUs)

	err = bootVM(ctx, ws, &spec)
	if err != nil {
		return err
	}

	err = manageInstallation(ctx, ws.ccvmDir, ws.instanceDir, ws)
	if err != nil {
		return err
	}

	fmt.Println("VM successfully created!")
	fmt.Println("Type ccloudvm connect to start using it.")

	return nil
}

// Start launches the VM
func Start(ctx context.Context, customSpec *VMSpec) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return err
	}
	in := &wkld.spec.VM

	err = in.mergeCustom(customSpec)
	if err != nil {
		return err
	}

	memGiB, CPUs := getMemAndCpus()
	if in.MemMiB == 0 {
		in.MemMiB = memGiB * 1024
	}
	if in.CPUs == 0 {
		in.CPUs = CPUs
	}

	if wkld.spec.NeedsNestedVM && !hostSupportsNestedKVM() {
		return fmt.Errorf("nested KVM is not enabled.  Please enable and try again")
	}

	if err := wkld.save(ws); err != nil {
		fmt.Printf("Warning: Failed to update instance state: %v", err)
	}

	fmt.Printf("Booting VM with %d MiB RAM and %d cpus\n", in.MemMiB, in.CPUs)

	err = bootVM(ctx, ws, in)
	if err != nil {
		return err
	}

	fmt.Println("VM Started")

	return nil
}

// Stop requests the VM shuts down cleanly
func Stop(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	err = stopVM(ctx, ws.instanceDir)
	if err != nil {
		return err
	}

	fmt.Println("VM Stopped")

	return nil
}

// Quit forceably kills VM
func Quit(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	err = quitVM(ctx, ws.instanceDir)
	if err != nil {
		return err
	}

	fmt.Println("VM Quit")

	return nil
}

// Status prints out VM information
func Status(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return errors.Wrap(err, "Unable to load instance state")
	}
	in := &wkld.spec.VM

	sshPort, err := in.sshPort()
	if err != nil {
		return fmt.Errorf("Instance does not have SSH port open.  Unable to determine status")
	}

	statusVM(ctx, ws.instanceDir, ws.keyPath, wkld.spec.WorkloadName,
		sshPort, in.Qemuport)
	return nil
}

func waitForSSH(ctx context.Context) (*workspace, int, error) {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return nil, 0, err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return nil, 0, errors.Wrap(err, "Unable to load instance state")
	}
	in := &wkld.spec.VM

	sshPort, err := in.sshPort()
	if err != nil {
		return nil, 0, err
	}

	if !sshReady(ctx, sshPort) {
		fmt.Printf("Waiting for VM to boot ")
	DONE:
		for {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return nil, 0, fmt.Errorf("Cancelled")
			}

			if sshReady(ctx, sshPort) {
				break DONE
			}

			fmt.Print(".")
		}
		fmt.Println()
	}

	return ws, sshPort, nil
}

// Run connects to the VM via SSH and runs the desired command
func Run(ctx context.Context, command string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("Unable to locate ssh binary")
	}

	ws, sshPort, err := waitForSSH(ctx)
	if err != nil {
		return err
	}

	args := []string{
		path,
		"-q", "-F", "/dev/null",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", ws.keyPath,
		"127.0.0.1", "-p", strconv.Itoa(sshPort),
	}

	if command != "" {
		args = append(args, command)
	}

	err = syscall.Exec(path, args, os.Environ())
	return err
}

// Connect opens a shell to the VM via
func Connect(ctx context.Context) error {
	return Run(ctx, "")
}

// Delete the VM
func Delete(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	_ = quitVM(ctx, ws.instanceDir)
	err = os.RemoveAll(ws.instanceDir)
	if err != nil {
		return errors.Wrap(err, "unable to delete instance")
	}

	return nil
}
