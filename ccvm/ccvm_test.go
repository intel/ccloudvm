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
	"testing"
	"time"
)

const standardTimeout = time.Second * 300

func TestSystem(t *testing.T) {
	ctx, cancelFunc := context.WithTimeout(context.Background(), standardTimeout)
	defer func() {
		cancelFunc()
	}()

	vmSpec := &VMSpec{MemGiB: 1, CPUs: 1}

	err := Create(ctx, "xenial", false, false, vmSpec)
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

	err = Quit(ctx)
	if err != nil {
		t.Errorf("Failed to Restart instance: %v", err)
	}

	err = Delete(context.Background())
	if err != nil {
		t.Errorf("Failed to Delete instance: %v", err)
	}
}
