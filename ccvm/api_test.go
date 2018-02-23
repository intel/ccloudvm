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
	"os"
	"sync"
	"testing"
	"time"

	"github.com/intel/ccloudvm/types"
)

var errCancelled = errors.New("Cancelled")

type testTrans struct {
	cancelFn context.CancelFunc
	resultCh chan interface{}
}

type testService struct {
	fail bool
}

func (s *testService) create(ctx context.Context, resultCh chan interface{}, args *types.CreateArgs) {
	if s.fail {
		resultCh <- fmt.Errorf("Create Failed")
		return
	}

	resultCh <- types.CreateResult{
		Name:     "test-instance",
		Finished: false,
		Line:     "Booting VM",
	}

	resultCh <- types.CreateResult{
		Name:     "test-instance",
		Finished: true,
	}
}

func (s *testService) stop(ctx context.Context, name string, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("Stop %s Failed", name)
		return
	}

	resultCh <- nil
}

func (s *testService) start(ctx context.Context, name string, args *types.VMSpec, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("Start %s Failed", name)
		return
	}

	resultCh <- nil
}

func (s *testService) quit(ctx context.Context, name string, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("Quit %s Failed", name)
		return
	}

	resultCh <- nil
}

func (s *testService) delete(ctx context.Context, name string, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("Delete %s Failed", name)
		return
	}

	resultCh <- nil
}

func (s *testService) status(ctx context.Context, name string, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("Status %s Failed", name)
		return
	}
	resultCh <- types.InstanceDetails{
		Name: "testInstance",
	}
}

func (s *testService) getInstances(ctx context.Context, resultCh chan interface{}) {
	if s.fail {
		resultCh <- fmt.Errorf("GetInstances Failed")
		return
	}

	resultCh <- []string{
		"vague-nimue",
		"worred-margawse",
	}
}

func startTestAPIServer(ts *testService, s *ServerAPI, wg *sync.WaitGroup, t *testing.T) {
	var transWg sync.WaitGroup
	transactions := make(map[int]testTrans)
	id := 0

	ctx, cancel := context.WithCancel(context.Background())

DONE:
	for {
		select {
		case <-s.signalCh:
			cancel()
			break DONE
		case action := <-s.actionCh:
			switch action := action.(type) {
			case startAction:
				transWg.Add(1)
				tCtx, tCancel := context.WithCancel(ctx)
				transactions[id] = testTrans{
					cancelFn: tCancel,
					resultCh: make(chan interface{}),
				}
				go func(ctx context.Context, tt testTrans) {
					select {
					case <-ctx.Done():
						tt.resultCh <- errCancelled
					case <-time.After(time.Millisecond * 100):
						action.action(ctx, ts, tt.resultCh)
					}
					tt.cancelFn()
					transWg.Done()
				}(tCtx, transactions[id])
				action.transCh <- id
				id++
			case cancelAction:
				tt, ok := transactions[int(action)]
				if !ok {
					t.Errorf("Unknown transaction %d", int(action))
				} else {
					tt.cancelFn()
				}
			case getResult:
				tt, ok := transactions[action.ID]
				if !ok {
					t.Errorf("Unknown transaction %d", action.ID)
					action.res <- fmt.Errorf("Unknown transaction %d", action.ID)
				} else {
					action.res <- tt.resultCh
				}
			case completeAction:
				_, ok := transactions[int(action)]
				if !ok {
					t.Errorf("Unknown transaction %d", int(action))
				} else {
					delete(transactions, int(action))
				}
			}
		}
	}

	if len(transactions) != 0 {
		t.Errorf("Transactions remaining %d", len(transactions))
	}

	transWg.Wait()
	wg.Done()
}

func testCreate(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Create(&types.CreateArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Create instance %v", err)
		return
	}

	for {
		var res types.CreateResult
		if err := api.CreateResult(id, &res); err != nil {
			t.Errorf("CreateResult failed %v", err)
			break
		}

		if res.Finished {
			break
		}
	}
}

func testDelete(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Delete("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Delete instance %v", err)
		return
	}

	var res struct{}
	if err := api.DeleteResult(id, &res); err != nil {
		t.Errorf("DeleteResult failed %v", err)
	}
}

func testStop(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Stop("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Stop instance %v", err)
		return
	}

	var res struct{}
	if err := api.StopResult(id, &res); err != nil {
		t.Errorf("StopResult failed %v", err)
	}
}

func testQuit(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Quit("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Quit instance %v", err)
		return
	}

	var res struct{}
	if err := api.QuitResult(id, &res); err != nil {
		t.Errorf("QuitResult failed %v", err)
	}
}

func testStart(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Start(&types.StartArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Start instance %v", err)
		return
	}

	var res struct{}
	if err := api.StartResult(id, &res); err != nil {
		t.Errorf("StartResult failed %v", err)
	}
}

func testGetInstanceDetails(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstanceDetails("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to retrieve instance details %v", err)
		return
	}

	if err := api.GetInstanceDetailsResult(id, &types.InstanceDetails{}); err != nil {
		t.Errorf("GetInstanceDetailsResult failed %v", err)
	}
}

func testGetInstances(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstances(struct{}{}, &id)
	if err != nil {
		t.Errorf("Failed to retrieve instances %v", err)
		return
	}

	if err := api.GetInstancesResult(id, &[]string{}); err != nil {
		t.Errorf("GetInstancesResult failed %v", err)
	}
}

func TestAPI(t *testing.T) {
	var wg sync.WaitGroup
	api := &ServerAPI{
		signalCh: make(chan os.Signal),
		actionCh: make(chan interface{}),
	}
	ts := &testService{}

	wg.Add(1)
	go startTestAPIServer(ts, api, &wg, t)

	t.Run("create", func(t *testing.T) {
		testCreate(t, api)
	})
	t.Run("delete", func(t *testing.T) {
		testDelete(t, api)
	})
	t.Run("quit", func(t *testing.T) {
		testQuit(t, api)
	})
	t.Run("stop", func(t *testing.T) {
		testStop(t, api)
	})
	t.Run("start", func(t *testing.T) {
		testStart(t, api)
	})
	t.Run("getinstancedetails", func(t *testing.T) {
		testGetInstanceDetails(t, api)
	})
	t.Run("getinstances", func(t *testing.T) {
		testGetInstances(t, api)
	})

	close(api.signalCh)

	wg.Wait()
}

func testCreateFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Create(&types.CreateArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Create instance %v", err)
		return
	}

	for {
		var res types.CreateResult
		if err := api.CreateResult(id, &res); err == nil {
			t.Errorf("CreateResult expected to fail")
			if res.Finished {
				break
			}
		} else {
			break
		}
	}
}

func testDeleteFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Delete("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Delete instance %v", err)
		return
	}

	var res struct{}
	if err := api.DeleteResult(id, &res); err == nil {
		t.Errorf("DeleteResult expected to fail")
	}
}

func testStopFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Stop("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Stop instance %v", err)
		return
	}

	var res struct{}
	if err := api.StopResult(id, &res); err == nil {
		t.Errorf("StopResult expected to fail")
	}
}

func testQuitFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Quit("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Quit instance %v", err)
		return
	}

	var res struct{}
	if err := api.QuitResult(id, &res); err == nil {
		t.Errorf("QuitResult expected to fail")
	}
}

func testStartFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Start(&types.StartArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Start instance %v", err)
		return
	}

	var res struct{}
	if err := api.StartResult(id, &res); err == nil {
		t.Errorf("StartResult expected to fail")
	}
}

func testGetInstanceDetailsFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstanceDetails("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to retrieve instance details %v", err)
		return
	}

	if err := api.GetInstanceDetailsResult(id, &types.InstanceDetails{}); err == nil {
		t.Errorf("GetInstanceDetailsResult expected to fail")
	}
}

func testGetInstancesFail(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstances(struct{}{}, &id)
	if err != nil {
		t.Errorf("Failed to retrieve instances %v", err)
		return
	}

	if err := api.GetInstancesResult(id, &[]string{}); err == nil {
		t.Errorf("GetInstancesResult expected to fail")
	}
}

func TestAPIFail(t *testing.T) {
	var wg sync.WaitGroup
	api := &ServerAPI{
		signalCh: make(chan os.Signal),
		actionCh: make(chan interface{}),
	}
	ts := &testService{
		fail: true,
	}

	wg.Add(1)
	go startTestAPIServer(ts, api, &wg, t)

	t.Run("create", func(t *testing.T) {
		testCreateFail(t, api)
	})
	t.Run("delete", func(t *testing.T) {
		testDeleteFail(t, api)
	})
	t.Run("quit", func(t *testing.T) {
		testQuitFail(t, api)
	})
	t.Run("stop", func(t *testing.T) {
		testStopFail(t, api)
	})
	t.Run("start", func(t *testing.T) {
		testStartFail(t, api)
	})
	t.Run("getinstancedetails", func(t *testing.T) {
		testGetInstanceDetailsFail(t, api)
	})
	t.Run("getinstances", func(t *testing.T) {
		testGetInstancesFail(t, api)
	})

	close(api.signalCh)

	wg.Wait()
}

func testCreateCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Create(&types.CreateArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Create instance %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	var res types.CreateResult
	for !res.Finished {
		if err := api.CreateResult(id, &res); err != nil {
			if err != errCancelled {
				t.Errorf("CreateResult expected to return cancelled")
			}
			break
		}
	}
}

func testDeleteCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Delete("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Delete instance %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	var res struct{}
	if err := api.DeleteResult(id, &res); err != nil && err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func testStopCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Stop("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Stop instance %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	var res struct{}
	if err := api.StopResult(id, &res); err != nil && err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func testQuitCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Quit("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to Quit instance %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	var res struct{}
	if err := api.QuitResult(id, &res); err != nil && err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func testStartCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.Start(&types.StartArgs{}, &id)
	if err != nil {
		t.Errorf("Failed to Start instance %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	var res struct{}
	if err := api.StartResult(id, &res); err != nil && err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func testGetInstanceDetailsCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstanceDetails("test-instance", &id)
	if err != nil {
		t.Errorf("Failed to retrieve instance details %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	if err := api.GetInstanceDetailsResult(id, &types.InstanceDetails{}); err != nil &&
		err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func testGetInstancesCancel(t *testing.T, api *ServerAPI) {
	var id int
	err := api.GetInstances(struct{}{}, &id)
	if err != nil {
		t.Errorf("Failed to retrieve instances %v", err)
		return
	}

	_ = api.Cancel(id, &struct{}{})

	if err := api.GetInstancesResult(id, &[]string{}); err == nil && err != errCancelled {
		t.Errorf("Expected Cancelled")
	}
}

func TestAPICancel(t *testing.T) {
	var wg sync.WaitGroup
	api := &ServerAPI{
		signalCh: make(chan os.Signal),
		actionCh: make(chan interface{}),
	}
	ts := &testService{}

	wg.Add(1)
	go startTestAPIServer(ts, api, &wg, t)

	t.Run("create", func(t *testing.T) {
		testCreateCancel(t, api)
	})
	t.Run("delete", func(t *testing.T) {
		testDeleteCancel(t, api)
	})
	t.Run("quit", func(t *testing.T) {
		testQuitCancel(t, api)
	})
	t.Run("stop", func(t *testing.T) {
		testStopCancel(t, api)
	})
	t.Run("start", func(t *testing.T) {
		testStartCancel(t, api)
	})
	t.Run("getinstancedetails", func(t *testing.T) {
		testGetInstanceDetailsCancel(t, api)
	})
	t.Run("getinstances", func(t *testing.T) {
		testGetInstancesCancel(t, api)
	})

	close(api.signalCh)

	wg.Wait()
}
