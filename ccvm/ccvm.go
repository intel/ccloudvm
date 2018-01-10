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
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
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

type packageUpgrade string

func (p *packageUpgrade) String() string {
	return string(*p)
}

func (p *packageUpgrade) Set(value string) error {
	if value != "true" && value != "false" {
		return fmt.Errorf("--package-update parameter should be true or false")
	}
	*p = packageUpgrade(value)
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
		return fmt.Errorf("Unable to access %s : %v", components[1], err)
	}
	*d = append(*d, drive{
		Path:    components[0],
		Format:  components[1],
		Options: strings.Join(components[2:], ","),
	})
	return nil
}

func vmFlags(fs *flag.FlagSet, customSpec *VMSpec) {
	fs.IntVar(&customSpec.MemGiB, "mem", customSpec.MemGiB, "Gigabytes of RAM allocated to VM")
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
		return fmt.Errorf("Unable to stat %s : %v", dir, err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	return nil
}

func createFlags(ctx context.Context, ws *workspace) (*workload, bool, error) {
	var debug bool
	var update packageUpgrade
	var customSpec VMSpec
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s create <workload> \n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  <workload>\tName of the workload to create\n\n")
		fs.PrintDefaults()
	}
	vmFlags(fs, &customSpec)
	fs.BoolVar(&debug, "debug", false, "Enables debug mode")
	fs.Var(&update, "package-upgrade",
		"Hint to enable or disable update of VM packages. Should be true or false")

	if err := fs.Parse(flag.Args()[1:]); err != nil {
		return nil, false, err
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return nil, false, errors.New("no workload specified")
	}
	workloadName := fs.Arg(0)

	wkl, err := createWorkload(ctx, ws, workloadName)
	if err != nil {
		return nil, false, err
	}

	in := &wkl.spec.VM

	err = in.mergeCustom(&customSpec)
	if err != nil {
		return nil, false, err
	}

	ws.Mounts = in.Mounts
	ws.Hostname = wkl.spec.Hostname
	if ws.NoProxy != "" {
		ws.NoProxy = fmt.Sprintf("%s,%s", ws.Hostname, ws.NoProxy)
	} else if ws.HTTPProxy != "" || ws.HTTPSProxy != "" {
		ws.NoProxy = ws.Hostname
	}
	ws.PackageUpgrade = string(update)

	return wkl, debug, nil
}

func startFlags(in *VMSpec) error {
	customSpec := *in

	fs := flag.NewFlagSet("start", flag.ExitOnError)
	vmFlags(fs, &customSpec)
	if err := fs.Parse(flag.Args()[1:]); err != nil {
		return err
	}

	return in.mergeCustom(&customSpec)
}

// Create sets up the VM
func Create(ctx context.Context) error {
	var err error

	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, debug, err := createFlags(ctx, ws)
	if err != nil {
		return err
	}

	if wkld.spec.NeedsNestedVM && !hostSupportsNestedKVM() {
		return fmt.Errorf("nested KVM is not enabled.  Please enable and try again")
	}

	in := &wkld.spec.VM
	_, err = os.Stat(ws.instanceDir)
	if err == nil {
		return fmt.Errorf("instance already exists")
	}

	fmt.Println("Installing host dependencies")
	installDeps(ctx)

	err = os.MkdirAll(ws.instanceDir, 0755)
	if err != nil {
		return fmt.Errorf("unable to create cache dir: %v", err)
	}

	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws.instanceDir)
		}
	}()

	err = wkld.save(ws)
	if err != nil {
		return fmt.Errorf("Unable to save instance state : %v", err)
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

	err = createRootfs(ctx, qcowPath, ws.instanceDir)
	if err != nil {
		return err
	}

	fmt.Printf("Booting VM with %d GB RAM and %d cpus\n", in.MemGiB, in.CPUs)

	err = bootVM(ctx, ws, in)
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
func Start(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return err
	}
	in := &wkld.spec.VM

	memGiB, CPUs := getMemAndCpus()
	if in.MemGiB == 0 {
		in.MemGiB = memGiB
	}
	if in.CPUs == 0 {
		in.CPUs = CPUs
	}

	err = startFlags(in)
	if err != nil {
		return err
	}

	if wkld.spec.NeedsNestedVM && !hostSupportsNestedKVM() {
		return fmt.Errorf("nested KVM is not enabled.  Please enable and try again")
	}

	if err := wkld.save(ws); err != nil {
		fmt.Printf("Warning: Failed to update instance state: %v", err)
	}

	fmt.Printf("Booting VM with %d GB RAM and %d cpus\n", in.MemGiB, in.CPUs)

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
		return fmt.Errorf("Unable to load instance state: %v", err)
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

// Connect to the VM via SSH
func Connect(ctx context.Context) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return fmt.Errorf("Unable to load instance state: %v", err)
	}
	in := &wkld.spec.VM

	path, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("Unable to locate ssh binary")
	}

	sshPort, err := in.sshPort()
	if err != nil {
		return err
	}

	if !sshReady(ctx, sshPort) {
		fmt.Printf("Waiting for VM to boot ")
	DONE:
		for {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return fmt.Errorf("Cancelled")
			}

			if sshReady(ctx, sshPort) {
				break DONE
			}

			fmt.Print(".")
		}
		fmt.Println()
	}

	err = syscall.Exec(path, []string{path,
		"-q", "-F", "/dev/null",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", ws.keyPath,
		"127.0.0.1", "-p", strconv.Itoa(sshPort)},
		os.Environ())
	return err
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
		return fmt.Errorf("unable to delete instance: %v", err)
	}

	return nil
}
