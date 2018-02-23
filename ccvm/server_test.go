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

package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/intel/ccloudvm/types"
)

type goodBackend struct {
	ipIndex  int
	reuseIps bool
}
type badBackend struct {
	failCreate bool
}

func (gb *goodBackend) createInstance(ctx context.Context, resultCh chan interface{},
	downloadCh chan<- downloadRequest, args *types.CreateArgs) error {
	return nil
}

func (gb *goodBackend) start(ctx context.Context, name string, args *types.VMSpec) error {
	return nil
}

func (gb *goodBackend) stop(ctx context.Context, name string) error {
	return nil
}

func (gb *goodBackend) quit(ctx context.Context, name string) error {
	return nil
}

func (gb *goodBackend) status(ctx context.Context, name string) (*types.InstanceDetails, error) {
	ip := 0x7f700000 + gb.ipIndex + 1
	a := byte((0xff000000 & ip) >> 24)
	b := byte((0xff0000 & ip) >> 16)
	c := byte((0xff00 & ip) >> 8)
	d := byte(0xff & ip)
	if !gb.reuseIps {
		gb.ipIndex++
	}

	return &types.InstanceDetails{
		Name: name,
		VMSpec: types.VMSpec{
			HostIP: net.IPv4(a, b, c, d),
		},
	}, nil
}

func (gb *goodBackend) deleteInstance(context.Context, string) error {
	return nil
}

func (bb *badBackend) createInstance(ctx context.Context, resultCh chan interface{},
	downloadCh chan<- downloadRequest, args *types.CreateArgs) error {
	if bb.failCreate {
		return errors.New("Failure")
	}
	return nil
}

func (bb *badBackend) start(ctx context.Context, name string, args *types.VMSpec) error {
	return errors.New("Failure")
}

func (bb *badBackend) stop(ctx context.Context, name string) error {
	return errors.New("Failure")
}

func (bb *badBackend) quit(ctx context.Context, name string) error {
	return errors.New("Failure")
}

func (bb *badBackend) status(ctx context.Context, name string) (*types.InstanceDetails, error) {
	return nil, errors.New("Failure")
}

func (bb *badBackend) deleteInstance(context.Context, string) error {
	return errors.New("Failure")
}

func setupServer(t *testing.T, b backend, wg *sync.WaitGroup) (string, chan interface{}, chan struct{}) {
	downloadCh := make(chan downloadRequest)
	actionCh := make(chan interface{})
	doneCh := make(chan struct{})
	ccvmDir, err := ioutil.TempDir("", "ccvm-test")
	if err != nil {
		t.Fatalf("Unable to create directory %v", err)
	}

	wg.Add(1)
	go func() {
		svc := &ccvmService{
			ccvmDir:       ccvmDir,
			downloadCh:    downloadCh,
			instances:     make(map[string]chan instanceCmd),
			instanceChMap: make(map[chan struct{}]string),
			hostIPs:       make(map[uint32]struct{}),
			hostIPMask:    0x7f000000 | uint32((os.Getuid()&0xffff)<<8),
			b:             b,
		}
		svc.run(doneCh, actionCh)
		wg.Done()
	}()

	return ccvmDir, actionCh, doneCh
}

func setupServerWithInstances(t *testing.T, b backend, wg *sync.WaitGroup, instances int) (string, chan interface{}, chan struct{}) {
	downloadCh := make(chan downloadRequest)
	actionCh := make(chan interface{})
	doneCh := make(chan struct{})
	ccvmDir, err := ioutil.TempDir("", "ccvm-test")
	if err != nil {
		t.Fatalf("Unable to create directory %v", err)
	}

	for i := 0; i < instances; i++ {
		name := makeRandomName()
		instanceDir := filepath.Join(ccvmDir, "instances", name)
		if err := os.MkdirAll(instanceDir, 0755); err != nil {
			_ = os.RemoveAll(ccvmDir)
			t.Fatal("Unable to create instance directory")
		}
	}

	wg.Add(1)
	go func() {
		svc := &ccvmService{
			ccvmDir:       ccvmDir,
			downloadCh:    downloadCh,
			instances:     make(map[string]chan instanceCmd),
			instanceChMap: make(map[chan struct{}]string),
			hostIPs:       make(map[uint32]struct{}),
			hostIPMask:    0x7f000000 | uint32((os.Getuid()&0xffff)<<8),
			b:             b,
		}
		svc.run(doneCh, actionCh)
		wg.Done()
	}()

	return ccvmDir, actionCh, doneCh
}

func TestServerShutdown(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, _, doneCh := setupServer(t, gb, &wg)
	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func checkResult(actionCh chan interface{}, id int, fail bool) error {
	res := make(chan interface{})
	actionCh <- getResult{
		ID:  id,
		res: res,
	}

	r := <-res
	if v, ok := r.(error); ok {
		return v
	}

	resultCh := r.(chan interface{})
	err, _ := (<-resultCh).(error)

	actionCh <- completeAction(id)

	if fail {
		if err == nil {
			return errors.New("Command expected to fail")
		}
		return nil
	}

	return err
}

func TestServerCommands(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{
				Name: "test-instance",
			})
		},
		transCh: transCh,
	}

	id := <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.stop(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.start(ctx, "test-instance", &types.VMSpec{}, resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, svc service, resultCh chan interface{}) {
			svc.status(ctx, "", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.quit(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.delete(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerFailedCommands(t *testing.T) {
	var wg sync.WaitGroup

	bb := &badBackend{}
	dir, actionCh, doneCh := setupServer(t, bb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{
				Name: "test-instance",
			})
		},
		transCh: transCh,
	}

	id := <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.stop(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.start(ctx, "test-instance", &types.VMSpec{}, resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, svc service, resultCh chan interface{}) {
			svc.status(ctx, "", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.quit(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.delete(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerFailedCreate(t *testing.T) {
	var wg sync.WaitGroup

	bb := &badBackend{true}
	dir, actionCh, doneCh := setupServer(t, bb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{})
		},
		transCh: transCh,
	}

	id := <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func getInstances(actionCh chan interface{}, transCh chan int) ([]string, error) {
	var instances []string

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.getInstances(ctx, resultCh)
		},
		transCh: transCh,
	}
	id := <-transCh

	resultChCh := make(chan interface{})
	actionCh <- getResult{
		ID:  id,
		res: resultChCh,
	}

	defer func() {
		actionCh <- completeAction(id)
	}()

	r := <-resultChCh
	if v, ok := r.(error); ok {
		return nil, fmt.Errorf("Failed to retrieve result channel: %v", v)
	}

	resultCh := r.(chan interface{})
	switch res := (<-resultCh).(type) {
	case error:
		return nil, fmt.Errorf("Failed to retrieve list of instances: %v", res)
	case []string:
		instances = res
	}

	return instances, nil
}

func TestServerCreateExisting(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServerWithInstances(t, gb, &wg, 5)

	transCh := make(chan int)
	res, err := getInstances(actionCh, transCh)
	if err != nil {
		t.Errorf(err.Error())
	}
	if len(res) != 5 {
		t.Errorf("Expected 5 instances found %d", len(res))
	}

	if len(res) > 0 {
		actionCh <- startAction{
			action: func(ctx context.Context, s service, resultCh chan interface{}) {
				s.create(ctx, resultCh, &types.CreateArgs{
					Name: res[0],
				})
			},
			transCh: transCh,
		}

		id := <-transCh
		if err := checkResult(actionCh, id, true); err != nil {
			t.Errorf(err.Error())
		}
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerCorruptInstances(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{
		reuseIps: true,
	}
	dir, actionCh, doneCh := setupServerWithInstances(t, gb, &wg, 5)

	transCh := make(chan int)
	res, err := getInstances(actionCh, transCh)
	if err != nil {
		t.Errorf(err.Error())
	}
	if len(res) != 1 {
		t.Errorf("Expected 1 instance found %d", len(res))
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerShutdownPending(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{})
		},
		transCh: transCh,
	}

	_ = <-transCh

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerCancelCreate(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{
				Name: "test-instance",
			})
		},
		transCh: transCh,
	}

	actionCh <- cancelAction(<-transCh)

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerCancelCommands(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.create(ctx, resultCh, &types.CreateArgs{
				Name: "test-instance",
			})
		},
		transCh: transCh,
	}

	id := <-transCh
	if err := checkResult(actionCh, id, false); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.stop(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	actionCh <- cancelAction(<-transCh)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.start(ctx, "test-instance", &types.VMSpec{}, resultCh)
		},
		transCh: transCh,
	}
	actionCh <- cancelAction(<-transCh)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.quit(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	actionCh <- cancelAction(<-transCh)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.delete(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	actionCh <- cancelAction(<-transCh)

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerNoInstance(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.stop(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id := <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.start(ctx, "test-instance", &types.VMSpec{}, resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.quit(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	actionCh <- startAction{
		action: func(ctx context.Context, s service, resultCh chan interface{}) {
			s.delete(ctx, "test-instance", resultCh)
		},
		transCh: transCh,
	}
	id = <-transCh
	if err := checkResult(actionCh, id, true); err != nil {
		t.Errorf(err.Error())
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}

func TestServerGetInstances(t *testing.T) {
	var wg sync.WaitGroup

	gb := &goodBackend{}
	dir, actionCh, doneCh := setupServer(t, gb, &wg)
	transCh := make(chan int)
	ids := make([]int, 2)

	for i := range ids {
		actionCh <- startAction{
			action: func(ctx context.Context, s service, resultCh chan interface{}) {
				s.create(ctx, resultCh, &types.CreateArgs{})
			},
			transCh: transCh,
		}
		ids[i] = <-transCh
	}

	/* Here we're testing parallel creation of instances */

	for i := range ids {
		if err := checkResult(actionCh, ids[i], false); err != nil {
			t.Errorf(err.Error())
		}
	}

	res, err := getInstances(actionCh, transCh)
	if err != nil {
		t.Errorf(err.Error())
	} else {
		if len(res) != 2 {
			t.Errorf("Expected two instances")
		} else if len(res[0]) == 0 || len(res[1]) == 0 {
			t.Errorf("Instance names are empty")
		}
	}

	close(doneCh)
	wg.Wait()
	_ = os.RemoveAll(dir)
}
