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
	"fmt"
	"os"

	"github.com/intel/ccloudvm/types"
)

// ServerAPI exposes an RPC based API to the cccloudvm client.  This API can
// be used to perform almost all ccloudvm actions.  The API has the following
// structure.  All APIs calls are asynchronous.  The client initiates a request
// and receives a transaction id.  At this stage the command is not processed.
// It's just scheduled.  The client can cancel the command by passing the transaction
// ID to the Cancel function or by calling the corresponding Result function
// to which he must also pass the transaction ID.  Some Result functions, e.g.,
// CreateResult need to be called multiple times.  The values returned from
// such functions will indicate whether they need to be called again or not.
// And example call sequence would be.
//
// Start -> id = 2
// StartResult(2)
//
type ServerAPI struct {
	signalCh chan os.Signal
	actionCh chan interface{}
}

func (s *ServerAPI) sendStartAction(fn func(context.Context, *service, chan interface{}), id *int) {
	action := startAction{
		action:  fn,
		transCh: make(chan int),
	}
	s.actionCh <- action
	*id = <-action.transCh
}

func (s *ServerAPI) voidResult(id int, reply *struct{}) error {
	result := getResult{
		ID:  id,
		res: make(chan interface{}),
	}

	s.actionCh <- result
	r := <-result.res
	if v, ok := r.(error); ok {
		return v
	}

	resultCh := r.(chan interface{})
	err, _ := (<-resultCh).(error)
	*reply = struct{}{}

	s.actionCh <- completeAction(id)

	return err
}

// Cancel can be used to cancel any command that has been issued but not
// yet completed.
func (s *ServerAPI) Cancel(arg int, reply *struct{}) error {
	fmt.Printf("Cancel(%d) called\n", arg)
	s.actionCh <- cancelAction(arg)
	*reply = struct{}{}
	return nil
}

// Create initiates a new instance creation request using the arguments provided by the
// args parameter. The value pointed to by id is set to the transaction ID of the request
// if no error occurs.
func (s *ServerAPI) Create(args *types.CreateArgs, id *int) error {
	fmt.Printf("Create %+v called\n", *args)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.create(ctx, resultCh, args)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// CreateResult blocks until information about the instance creation request has
// been received.  This information could be an error, signalling that the
// request has failed or a types.CreateResult.  CreateResult should be called continually
// until res.Finished == true.  If successful, the final types.CreateResult returned will
// have the its Finished field set to true and its Name field set to the name of the instance.
func (s *ServerAPI) CreateResult(id int, res *types.CreateResult) error {
	var err error

	fmt.Printf("CreateResult(%d) called\n", id)

	result := getResult{
		ID:  id,
		res: make(chan interface{}),
	}

	s.actionCh <- result
	r := <-result.res
	if v, ok := r.(error); ok {
		fmt.Printf("CreateResult(%d) finished: %v\n", id, err)
		return v
	}

	resultCh := r.(chan interface{})
	switch v := (<-resultCh).(type) {
	case types.CreateResult:
		*res = v
		if !res.Finished {
			return nil
		}
	case error:
		err = v
	}

	s.actionCh <- completeAction(id)

	fmt.Printf("CreateResult(%d) finished: %v\n", id, err)

	return err
}

// Stop initiates a request to stop an instance.
func (s *ServerAPI) Stop(instanceName string, id *int) error {
	fmt.Printf("Stop [%s] called\n", instanceName)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.stop(ctx, instanceName, resultCh)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// StopResult blocks until the instance has been stopped or an error has occurred.
func (s *ServerAPI) StopResult(id int, reply *struct{}) error {
	fmt.Printf("StopResult(%d) called\n", id)

	err := s.voidResult(id, reply)

	fmt.Printf("StopResult(%d) finished: %v\n", id, err)
	return err
}

// Start initiates a request to start an instance.
func (s *ServerAPI) Start(args *types.StartArgs, id *int) error {
	fmt.Printf("Start [%s] called\n", args.Name)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.start(ctx, args.Name, &args.VMSpec, resultCh)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// StartResult blocks until the instance has been started or an error occurs.
func (s *ServerAPI) StartResult(id int, reply *struct{}) error {
	fmt.Printf("StartResult(%d) called\n", id)

	err := s.voidResult(id, reply)

	fmt.Printf("StartResult(%d) finished: %v\n", id, err)
	return err
}

// Quit initiates a request to forcefully quit an instance.
func (s *ServerAPI) Quit(instanceName string, id *int) error {
	fmt.Printf("Quit [%s] called\n", instanceName)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.quit(ctx, instanceName, resultCh)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// QuitResult blocks until the instance has been quit or an error occurs.
func (s *ServerAPI) QuitResult(id int, reply *struct{}) error {
	fmt.Printf("QuitResult(%d) called\n", id)

	err := s.voidResult(id, reply)

	fmt.Printf("QuitResult(%d) finished: %v\n", id, err)
	return err
}

// Delete initiates a request to delete an instance.
func (s *ServerAPI) Delete(instanceName string, id *int) error {
	fmt.Printf("Delete [%s] called\n", instanceName)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.delete(ctx, instanceName, resultCh)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// DeleteResult blocks until the instance has been deleted or an error has occurred.
func (s *ServerAPI) DeleteResult(id int, reply *struct{}) error {
	fmt.Printf("DeleteResult(%d) called\n", id)

	err := s.voidResult(id, reply)

	fmt.Printf("DeleteResult(%d) finished: %v\n", id, err)
	return err
}

// GetInstanceDetails initiates a request to retrieve information about an instance.
func (s *ServerAPI) GetInstanceDetails(instanceName string, id *int) error {
	fmt.Printf("GetInstanceDetails [%s] called\n", instanceName)

	s.sendStartAction(func(ctx context.Context, svc *service, resultCh chan interface{}) {
		svc.status(ctx, instanceName, resultCh)
	}, id)

	fmt.Printf("Transaction ID %d\n", *id)
	return nil
}

// GetInstanceDetailsResult blocks until the instance's details have been received or
// an error occurs.
func (s *ServerAPI) GetInstanceDetailsResult(id int, reply *types.InstanceDetails) error {
	fmt.Printf("GetInstanceDetails(%d) called\n", id)

	result := getResult{
		ID:  id,
		res: make(chan interface{}),
	}

	s.actionCh <- result
	r := <-result.res
	if v, ok := r.(error); ok {
		fmt.Printf("GetInstanceDetailsResult(%d) finished: %v\n", id, v)
		return v
	}

	var err error

	resultCh := r.(chan interface{})
	switch res := (<-resultCh).(type) {
	case error:
		err = res
	case types.InstanceDetails:
		*reply = res
	}
	s.actionCh <- completeAction(id)

	fmt.Printf("GetInstanceDetailsResult(%d) finished: %v\n", id, err)

	return err
}
