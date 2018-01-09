/*
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
*/

/* TODO

5. Install kernel
12. Make most output from osprepare optional
*/

package ccloudvm

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
)

// Different types of virtual development environments
// we support.
const (
	CIAO            = "ciao"
	CLEARCONTAINERS = "clearcontainers"
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
		return fmt.Errorf("Unable to access %s : %v", components[1], err)
	}
	*d = append(*d, drive{
		Path:    components[0],
		Format:  components[1],
		Options: strings.Join(components[2:], ","),
	})
	return nil
}

// VMFlags populates the provided FlagSet with common flags for start and create
func VMFlags(fs *flag.FlagSet, spec *VMSpec) {
	fs.IntVar(&spec.MemGiB, "mem", 0, "Gigabytes of RAM allocated to VM")
	fs.IntVar(&spec.CPUs, "cpus", 0, "VCPUs assigned to VM")
	fs.Var(&spec.Mounts, "mount", "directory to mount in guest VM via 9p. Format is tag,security_model,path")
	fs.Var(&spec.Drives, "drive", "Host accessible resource to appear as block device in guest VM.  Format is path,format[,option]*")
	fs.Var(&spec.PortMappings, "port", "port mapping. Format is host_port-guest_port, e.g., -port 10022-22")
	fs.UintVar(&spec.Qemuport, "qemuport", 0, "Port to follow qemu logs of the guest machine, eg., --qemuport=9999")
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

func prepareCreate(ctx context.Context, ws *workspace, workloadName string, customSpec *VMSpec, debug bool, packageUpgrade bool) (*workload, bool, error) {
	for i := range customSpec.Mounts {
		if err := checkDirectory(customSpec.Mounts[i].Path); err != nil {
			return nil, false, err
		}
	}

	wkl, err := createWorkload(ctx, ws, workloadName)
	if err != nil {
		return nil, false, err
	}

	in := &wkl.spec.VM
	if customSpec.MemGiB != 0 {
		in.MemGiB = customSpec.MemGiB
	}
	if customSpec.CPUs != 0 {
		in.CPUs = customSpec.CPUs
	}
	if customSpec.Qemuport != 0 {
		in.Qemuport = customSpec.Qemuport
	}

	in.mergeMounts(customSpec.Mounts)
	in.mergePorts(customSpec.PortMappings)
	in.mergeDrives(customSpec.Drives)

	ws.Mounts = in.Mounts
	ws.Hostname = wkl.spec.Hostname
	if ws.NoProxy != "" {
		ws.NoProxy = fmt.Sprintf("%s,%s", ws.Hostname, ws.NoProxy)
	} else if ws.HTTPProxy != "" || ws.HTTPSProxy != "" {
		ws.NoProxy = ws.Hostname
	}

	ws.PackageUpgrade = "false"
	if packageUpgrade {
		ws.PackageUpgrade = "true"
	}

	return wkl, debug, nil
}

func prepareStart(in *VMSpec, customSpec *VMSpec) error {
	for i := range customSpec.Mounts {
		if err := checkDirectory(customSpec.Mounts[i].Path); err != nil {
			return err
		}
	}

	if customSpec.MemGiB != 0 {
		in.MemGiB = customSpec.MemGiB
	}
	if customSpec.CPUs != 0 {
		in.CPUs = customSpec.CPUs
	}
	if customSpec.Qemuport != 0 {
		in.Qemuport = customSpec.Qemuport
	}

	in.mergeMounts(customSpec.Mounts)
	in.mergePorts(customSpec.PortMappings)
	in.mergeDrives(customSpec.Drives)

	return nil
}

// Create creates the desired VM using the provided parameters
func Create(ctx context.Context, workloadName string, customSpec *VMSpec, debug bool, packageUpgrade bool) error {
	var err error

	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, debug, err := prepareCreate(ctx, ws, workloadName, customSpec, debug, packageUpgrade)
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

// Start the VM
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

	memGiB, CPUs := getMemAndCpus()
	if in.MemGiB == 0 {
		in.MemGiB = memGiB
	}
	if in.CPUs == 0 {
		in.CPUs = CPUs
	}

	err = prepareStart(in, customSpec)
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

// Stop the VM (cleanly)
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

// Quit the VM (forceably)
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

// Status returns information about the VM
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

// Connect starts an SSH session to the VM
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

// Delete quits and then removes the VM
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
