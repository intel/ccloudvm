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
	"context"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"github.com/intel/ccloudvm/types"
	"github.com/pmezard/go-difflib/difflib"
)

const document1 = `# Just a simple document
foo: "a string"
bar: True
`

const document2 = `# A list
- foo
- bar
`

const twoDocuments = "---\n" + document1 + "...\n----\n" + document2 + "...\n"
const twoDocumentsSimplified = document1 + "---\n" + document2

func toStringSlice(s [][]byte) []string {
	ss := make([]string, len(s))
	for i := range s {
		ss[i] = string(s[i])
	}
	return ss
}

func TestSplitYaml(t *testing.T) {
	tests := []struct {
		content   string
		documents [][]byte
	}{
		{"", [][]byte{}},
		{document1, [][]byte{[]byte(document1)}},
		{twoDocuments, [][]byte{[]byte(document1), []byte(document2)}},
		{twoDocumentsSimplified, [][]byte{[]byte(document1), []byte(document2)}},
	}

	for i := range tests {
		test := &tests[i]

		documents := splitYaml([]byte(test.content))
		diff, _ := difflib.GetContextDiffString(difflib.ContextDiff{
			A:        toStringSlice(test.documents),
			B:        toStringSlice(documents),
			FromFile: "Expected",
			ToFile:   "Got",
		})
		if diff != "" {
			t.Errorf("%s", diff)
		}
	}
}

func TestCreateWorkload(t *testing.T) {
	ccvmDir, err := ioutil.TempDir("", "ccloudvm-tests-")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	defer func() {
		if err := os.RemoveAll(ccvmDir); err != nil {
			t.Errorf("Failed to remove %s : %v", ccvmDir, err)
		}
	}()

	ws, err := createMockWorkSpaceWithWorkload(sampleWorkload, "workload", ccvmDir)
	if err != nil {
		t.Errorf("Failed to create mock workload : %v", err)
	} else {
		workload, err := createWorkload(context.Background(), ws, "workload")
		if err != nil {
			t.Errorf("Unable to create workload : %v", err)
		} else {
			if !reflect.DeepEqual(mockVMSpec, workload.spec.VM) {
				t.Errorf("Expected %+v got %+v", mockVMSpec, workload.spec.VM)
			}
		}
	}

	ws, err = createMockWorkSpaceWithWorkload("\n---\n", "workload", ccvmDir)
	if err != nil {
		t.Errorf("Failed to create mock workload : %v", err)
	} else {
		workload, err := createWorkload(context.Background(), ws, "workload")
		if err != nil {
			t.Errorf("Unable to create workload : %v", err)
		} else {
			if workload.spec.VM.DiskGiB != 60 {
				t.Errorf("Disk size should be set to default of %d", 60)
			}
			if workload.spec.VM.CPUs == 0 {
				t.Errorf("CPUs should be greater than 0")
			}
			if workload.spec.VM.MemMiB == 0 {
				t.Errorf("Memory should be greater than 0")
			}
			if len(workload.spec.VM.PortMappings) == 0 {
				t.Errorf("Theres should be at least one port mapped for SSH")
			}
		}
	}
}

func TestRestoreWorkload(t *testing.T) {
	workloads := []string{
		sampleWorkload,
	}

	ccvmDir, err := ioutil.TempDir("", "ccloudvm-tests-")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	defer func() {
		if err := os.RemoveAll(ccvmDir); err != nil {
			t.Errorf("Failed to remove %s : %v", ccvmDir, err)
		}
	}()

	for i := range workloads {
		ws, err := createMockWorkSpaceWithInstance(workloads[i], ccvmDir)
		if err != nil {
			t.Errorf("Failed to create mock workload : %v", err)
			continue
		}

		workload, err := restoreWorkload(ws)
		if err != nil {
			t.Errorf("Unable to restore workload %d : %v", i, err)
			continue
		}

		if !reflect.DeepEqual(mockVMSpec, workload.spec.VM) {
			t.Errorf("Expected %+v got %+v", mockVMSpec, workload.spec.VM)
			continue
		}

		if guestDownloadURL != workload.spec.BaseImageURL {
			t.Errorf("URLs do not match expected %s got %s",
				guestDownloadURL, workload.spec.BaseImageURL)
		}
		if guestImageFriendlyName != workload.spec.BaseImageName {
			t.Errorf("Names do not match expected %s got %s",
				guestImageFriendlyName, workload.spec.BaseImageName)
		}
		if defaultHostname != workload.spec.Hostname {
			t.Errorf("Hostnames do not match expected %s got %s",
				defaultHostname, workload.spec.Hostname)
		}

	}
}

var level0Document = `
base_image_url: https://mirror.us-midwest-1.nexcess.net/fedora/releases/27/CloudImages/x86_64/images/Fedora-Cloud-Base-27-1.6.x86_64.qcow2
base_image_name: Fedora 27
needs_nested_vm: true
...
---
base: value
`

var level1Document = `
vm:
  mem_mib: 128
  cpus: 2
  mounts:
  - tag: hostgo
    security_model: passthrough
    path: /tmp
inherits: level0
...
---
#cloud-config
seq:
- command 1
- command 2
map:
 key1: value1
`

var level2Document = `
vm:
  mem_mib: 256
inherits: level1
...
---
seq:
- command 3
map:
  key2: value2
extra: value
`

func level0spec() workloadSpec {
	spec := workloadSpec{
		BaseImageURL:  "https://mirror.us-midwest-1.nexcess.net/fedora/releases/27/CloudImages/x86_64/images/Fedora-Cloud-Base-27-1.6.x86_64.qcow2",
		BaseImageName: "Fedora 27",
		NeedsNestedVM: true,
		VM:            defaultVMSpec(),
		WorkloadName:  "level0",
		Hostname:      "singlevm",
	}

	spec.ensureSSHPortMapping()

	return spec
}

func level1spec() workloadSpec {
	spec := level0spec()
	spec.VM.MemMiB = 128
	spec.VM.CPUs = 2
	spec.VM.Mounts = []types.Mount{
		{
			Tag:           "hostgo",
			SecurityModel: "passthrough",
			Path:          "/tmp",
		},
	}
	spec.Inherits = "level0"
	spec.WorkloadName = "level1"

	return spec
}

func level2spec() workloadSpec {
	spec := level1spec()
	spec.VM.MemMiB = 256
	spec.Inherits = "level1"
	spec.WorkloadName = "level2"

	return spec
}

var level0cloudConfig = `#cloud-config
base: value
`

var level1cloudConfig = `#cloud-config
base: value
map:
  key1: value1
seq:
- command 1
- command 2
`

var level2cloudConfig = `#cloud-config
base: value
extra: value
map:
  key1: value1
  key2: value2
seq:
- command 1
- command 2
- command 3
`

func TestWorkloadInheritance(t *testing.T) {
	workloads := []struct {
		body        string
		name        string
		expected    workloadSpec
		cloudConfig string
	}{
		{
			level0Document,
			"level0",
			level0spec(),
			level0cloudConfig,
		},
		{
			level1Document,
			"level1",
			level1spec(),
			level1cloudConfig,
		},
		{
			level2Document,
			"level2",
			level2spec(),
			level2cloudConfig,
		},
	}

	ccvmDir, err := ioutil.TempDir("", "ccloudvm-tests-")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	defer func() {
		if err := os.RemoveAll(ccvmDir); err != nil {
			t.Errorf("Failed to remove %s : %v", ccvmDir, err)
		}
	}()

	var ws *workspace
	for i := range workloads {
		ws, err = createMockWorkSpaceWithWorkload(workloads[i].body, workloads[i].name, ccvmDir)
		if err != nil {
			t.Errorf("Failed to create mock workload: %v", err)
			break
		}
	}

	if err != nil {
		t.Fatalf("Error populating mock workloads: %v", err)
	}

	for i := range workloads {
		wkld, err := createWorkload(context.TODO(), ws, workloads[i].name)
		if err != nil {
			t.Fatalf("Error creating workloads: %v", err)
		}

		if !reflect.DeepEqual(wkld.spec, workloads[i].expected) {
			t.Fatalf("Created workload not as expected: %s", workloads[i].name)
		}

		err = wkld.generateCloudConfig(ws)
		if err != nil {
			t.Fatalf("Error generating cloud config data: %v", err)
		}

		if string(wkld.mergedUserData) != workloads[i].cloudConfig {
			t.Fatalf("Merged data doesn't match expected: %s vs %s", string(wkld.mergedUserData), workloads[i].cloudConfig)
		}
	}
}

var emptyWorkloadDocument = `
...
---
`

func TestBaseWorkload(t *testing.T) {
	ccvmDir, err := ioutil.TempDir("", "ccloudvm-tests-")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	defer func() {
		if err := os.RemoveAll(ccvmDir); err != nil {
			t.Errorf("Failed to remove %s : %v", ccvmDir, err)
		}
	}()

	ws, err := createMockWorkSpaceWithWorkload(emptyWorkloadDocument, "", ccvmDir)
	if err != nil {
		t.Fatalf("Failed to create mock workload: %v", err)
	}

	wkld, err := createWorkload(context.Background(), ws, "")
	if err != nil {
		t.Fatalf("Error creating workloads: %v", err)
	}

	defaultWorkload := defaultWorkload()
	defaultWorkload.spec.ensureSSHPortMapping()

	if !reflect.DeepEqual(defaultWorkload, wkld) {
		t.Fatalf("Default workload expected")
	}
}
