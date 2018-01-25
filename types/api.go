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

package types

// CreateArgs contains all the information necessary to create a new
// ccloudvm instance.
type CreateArgs struct {
	WorkloadName string
	Debug        bool
	Update       bool
	CustomSpec   VMSpec
	HTTPProxy    string
	HTTPSProxy   string
	NoProxy      string
}

// CreateResult contains information about the status of an instance
// creation request.  It has two fields. Finished, if true, indicates
// that the creation request has finished and Line containing a lines
// of output.
type CreateResult struct {
	Finished bool
	Line     string
}

// SSHDetails contains SSH connection information for an instance
type SSHDetails struct {
	KeyPath string
	Port    int
}

// InstanceDetails contains information about an instance
type InstanceDetails struct {
	SSH       SSHDetails
	Workload  string
	DebugPort uint
}
