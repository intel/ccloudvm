//
// Copyright (c) 2018 Intel Corporation
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

package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/ciao-project/ciao/osprepare"
	"github.com/intel/ccloudvm/ccvm"
	"github.com/intel/ccloudvm/types"
)

type logger struct{}

func (l logger) V(int32) bool {
	return false
}

func (l logger) Infof(s string, args ...interface{}) {
	out := fmt.Sprintf(s, args...)
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
}

func (l logger) Warningf(s string, args ...interface{}) {
	l.Infof(s, args)
}

func (l logger) Errorf(s string, args ...interface{}) {
	l.Infof(s, args)
}

// Setup Installs dependencies
func Setup(ctx context.Context) error {
	fmt.Println("Installing host dependencies")
	osprepare.InstallDeps(ctx, ccloudvmDeps, logger{})
	return nil
}

// Create sets up the VM
func Create(ctx context.Context, workloadName string, debug bool, update bool, customSpec *types.VMSpec) error {
	return ccvm.Create(ctx, workloadName, debug, update, customSpec)
}

// Start launches the VM
func Start(ctx context.Context, customSpec *types.VMSpec) error {
	return ccvm.Start(ctx, customSpec)
}

// Stop requests the VM shuts down cleanly
func Stop(ctx context.Context) error {
	return ccvm.Stop(ctx)
}

// Quit forceably kills VM
func Quit(ctx context.Context) error {
	return ccvm.Quit(ctx)
}

// Status prints out VM information
func Status(ctx context.Context) error {
	return ccvm.Status(ctx)
}

// Run connects to the VM via SSH and runs the desired command
func Run(ctx context.Context, command string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("Unable to locate ssh binary")
	}

	keyPath, sshPort, err := ccvm.WaitForSSH(ctx)
	if err != nil {
		return err
	}

	args := []string{
		path,
		"-q", "-F", "/dev/null",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", keyPath,
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
	return ccvm.Delete(ctx)
}
