//
// Copyright (c) 2017 Intel Corporation
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
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

const xenialWorkloadSpec = `
base_image_url: ` + guestDownloadURL + `
base_image_name: ` + guestImageFriendlyName + `
vm:
  mem_mib: 3072
  cpus: 2
  ports:
  - host: 10022
    guest: 22
  mounts: []
  disk_gib: 60
hostname: singlevm
`

var mockVMSpec = types.VMSpec{
	MemMiB:       3072,
	CPUs:         2,
	DiskGiB:      defaultRootFSSize,
	PortMappings: []types.PortMapping{{Host: 10022, Guest: 22}},
	Mounts:       []types.Mount{},
}

const sampleCloudInit = `
`

const sampleWorkload = "---\n" + xenialWorkloadSpec + "...\n---\n" + sampleCloudInit + "...\n"

func createMockWorkSpaceWithWorkload(workload, workloadName, ccvmDir string) (*workspace, error) {
	workloadDir := filepath.Join(ccvmDir, "workloads")
	err := os.MkdirAll(workloadDir, 0750)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to create directory %s", workloadDir)
	}

	ws := &workspace{
		ccvmDir: ccvmDir,
	}

	workloadFile := path.Join(workloadDir, workloadName+".yaml")
	err = ioutil.WriteFile(workloadFile, []byte(workload), 0640)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to write workload file %s", workloadFile)
	}

	return ws, nil
}

func createMockWorkSpaceWithInstance(workload, ccvmDir string) (*workspace, error) {
	instanceDir, err := ioutil.TempDir(ccvmDir, "wkl-")
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to create directory %s", instanceDir)
	}

	ws := &workspace{
		ccvmDir:     ccvmDir,
		instanceDir: instanceDir,
	}

	workloadFile := path.Join(ws.instanceDir, "state.yaml")
	err = ioutil.WriteFile(workloadFile, []byte(workload), 0640)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to write workload file %s", workloadFile)
	}

	return ws, nil
}
