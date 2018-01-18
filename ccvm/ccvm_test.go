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

package ccvm

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const standardTimeout = time.Second * 300

func setupDownloadDir(t *testing.T) (string, string) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatalf("Unable to determine home directory")
	}

	tmpDir, err := ioutil.TempDir(home, "ccloudvmtest-")
	if err != nil {
		t.Fatalf("Unable to create temporary directory: %v", err)
	}

	downDir := filepath.Join(tmpDir, "down")
	err = os.Mkdir(downDir, 0755)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Unable to create download directory %s: %v", downDir, err)
	}

	return tmpDir, downDir
}

func TestSystem(t *testing.T) {
	ctx, cancelFunc := context.WithTimeout(context.Background(), standardTimeout)
	defer func() {
		cancelFunc()
	}()

	// Create a directory which will be mounted inside the newly created
	// instance.  This directory will contain a sub directory called down
	// into which the instance will download the README.md file of this
	// project.  At the end of the test we check for the existence of
	// this file on the host, checking download and mounting.  This test
	// requires the cooperation of the semaphore.yaml workload.

	tmpDir, downDir := setupDownloadDir(t)
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	vmSpec := &VMSpec{
		MemGiB: 1,
		CPUs:   1,
		Mounts: []mount{
			{
				Tag:           "tmpdir",
				SecurityModel: "passthrough",
				Path:          tmpDir,
			},
		},
	}

	err := Create(ctx, "semaphore", false, false, vmSpec)
	if err != nil {
		t.Fatalf("Unable to create VM: %v", err)
	}

	err = Start(ctx, vmSpec)
	if err == nil || err == context.DeadlineExceeded {
		t.Errorf("Start expected to fail")
	}

	err = Stop(ctx)
	if err != nil {
		t.Errorf("Failed to Stop instance: %v", err)
	}

	err = Start(ctx, vmSpec)
	if err != nil {
		t.Errorf("Failed to Restart instance: %v", err)
	}

	_, _, err = waitForSSH(ctx)
	if err != nil {
		t.Errorf("Instance not available via SSH: %v", err)
	}

	err = Run(ctx, "echo $PATH")
	if err != nil {
		t.Errorf("Failed to run test command via SSH")
	}

	err = Quit(ctx)
	if err != nil {
		t.Errorf("Failed to Restart instance: %v", err)
	}

	err = Delete(context.Background())
	if err != nil {
		t.Errorf("Failed to Delete instance: %v", err)
	}

	readmePath := filepath.Join(downDir, "README.md")
	_, err = os.Stat(readmePath)
	if err != nil {
		t.Errorf("Expected %s to exist: %v", readmePath, err)
	}
}
