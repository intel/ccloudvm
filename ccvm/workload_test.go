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
	"reflect"
	"testing"

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

func TestRestoreWorkload(t *testing.T) {
	tests := []struct {
		checkSpec bool // Should we check the workload.spec content?
		workload  string
	}{
		// 1 document: per-VM data (legacy)
		{workload: sampleVMSpec},
		// 2 documents: spec, cloud init file
		{workload: sampleWorkload, checkSpec: true},
		// 3 documents: spec, per-VM data, cloud init file (legacy)
		{workload: sampleWorkload3Docs, checkSpec: true},
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

	for i := range tests {
		test := &tests[i]

		ws, err := createMockWorkSpaceWithWorkload(test.workload, ccvmDir)
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

		if test.checkSpec {
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
}
