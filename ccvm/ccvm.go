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

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

func prepareCreate(ctx context.Context, args *types.CreateArgs) (*workload, *workspace, error) {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return nil, nil, err
	}

	ws.HTTPProxy = args.HTTPProxy
	ws.HTTPSProxy = args.HTTPSProxy
	ws.NoProxy = args.NoProxy

	wkld, err := createWorkload(ctx, ws, args.WorkloadName)
	if err != nil {
		return nil, nil, err
	}

	in := &wkld.spec.VM

	err = in.MergeCustom(&args.CustomSpec)
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
	if args.Update {
		ws.PackageUpgrade = "true"
	}

	if wkld.spec.NeedsNestedVM && !hostSupportsNestedKVM() {
		return nil, nil, fmt.Errorf("nested KVM is not enabled.  Please enable and try again")
	}

	return wkld, ws, nil
}

// Create sets up the VM
func Create(ctx context.Context, resultCh chan interface{}, args *types.CreateArgs) error {
	var err error

	wkld, ws, err := prepareCreate(ctx, args)
	if err != nil {
		return err
	}

	_, err = os.Stat(ws.instanceDir)
	if err == nil {
		return fmt.Errorf("instance already exists")
	}

	err = os.MkdirAll(ws.instanceDir, 0755)
	if err != nil {
		return errors.Wrap(err, "unable to create cache dir")
	}

	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws.instanceDir)
		}
	}()

	err = prepareSSHKeys(ctx, ws)
	if err != nil {
		return err
	}

	err = wkld.generateCloudConfig(ws)
	if err != nil {
		return errors.Wrap(err, "Error applying template to user-data")
	}

	err = wkld.save(ws)
	if err != nil {
		return errors.Wrap(err, "Unable to save instance state")
	}

	resultCh <- fmt.Sprintf("Downloading %s\n", wkld.spec.BaseImageName)
	qcowPath, err := downloadFile(ctx, wkld.spec.BaseImageURL, ws.ccvmDir, downloadProgress)
	if err != nil {
		return err
	}

	err = buildISOImage(ctx, resultCh, wkld.mergedUserData, ws, args.Debug)
	if err != nil {
		return err
	}

	spec := wkld.spec.VM
	err = createRootfs(ctx, qcowPath, ws.instanceDir, spec.DiskGiB)
	if err != nil {
		return err
	}

	resultCh <- fmt.Sprintf("Booting VM with %d MiB RAM and %d cpus\n", spec.MemMiB, spec.CPUs)

	err = bootVM(ctx, ws, &spec)
	if err != nil {
		return err
	}

	err = manageInstallation(ctx, resultCh, ws.ccvmDir, ws.instanceDir, ws)
	if err != nil {
		return err
	}

	resultCh <- fmt.Sprintf("VM successfully created!\n")
	resultCh <- fmt.Sprintf("Type ccloudvm connect to start using it.\n")

	return nil
}

// Start launches the VM
func Start(ctx context.Context, customSpec *types.VMSpec) error {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return err
	}
	in := &wkld.spec.VM

	err = in.MergeCustom(customSpec)
	if err != nil {
		return err
	}

	memMiB, CPUs := getMemAndCpus()
	if in.MemMiB == 0 {
		in.MemMiB = memMiB
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
func Status(ctx context.Context) (*types.InstanceDetails, error) {
	ws, err := prepareEnv(ctx)
	if err != nil {
		return nil, err
	}

	wkld, err := restoreWorkload(ws)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to load instance state")
	}
	in := &wkld.spec.VM

	sshPort, err := in.SSHPort()
	if err != nil {
		return nil, fmt.Errorf("Instance does not have SSH port open.  Unable to determine status")
	}

	return &types.InstanceDetails{
		SSH: types.SSHDetails{
			KeyPath: ws.keyPath,
			Port:    sshPort,
		},
		Workload:  wkld.spec.WorkloadName,
		DebugPort: in.Qemuport,
	}, nil
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
