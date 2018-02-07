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
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/activation"
	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
)

// Indicies of the first three channels in service.cases
const (
	DoneChIndex = iota
	ActionChIndex
	TimeChIndex
)

var systemd bool

func init() {
	flag.BoolVar(&systemd, "systemd", true, "Use systemd socket activation if true")
}

type startAction struct {
	action  func(ctx context.Context, s *service, resultCh chan interface{})
	transCh chan int
}

type getResult struct {
	ID  int
	res chan interface{}
}

type cancelAction int
type completeAction int

type transaction struct {
	ctx      context.Context
	cancel   func()
	resultCh chan interface{}
}

const (
	instanceCmdOther = iota
	instanceCmdCreate
	instanceCmdDelete
)

type instanceCmd struct {
	cmdType  int
	resultCh chan interface{}
	fn       func() error
}

type service struct {
	ccvmDir       string
	downloadCh    chan<- downloadRequest
	counter       int
	shutdownTimer *time.Timer
	transactions  map[int]transaction
	shuttingDown  bool
	cases         []reflect.SelectCase
	instances     map[string]chan instanceCmd
	instanceChMap map[chan struct{}]chan instanceCmd
	instanceWg    sync.WaitGroup
}

func instanceLoop(instanceCh <-chan instanceCmd, closeCh chan struct{}, wg *sync.WaitGroup) {
	deleted := false
	for cmd := range instanceCh {
		switch cmd.cmdType {
		case instanceCmdCreate:
			if deleted {
				cmd.resultCh <- errors.New("Instance already exists (but is being deleted)")
				continue
			}
			err := cmd.fn()
			cmd.resultCh <- err
			if err != nil {
				deleted = true
				close(closeCh)
			}
		case instanceCmdDelete:
			if deleted {
				cmd.resultCh <- errors.New("Instance does not exist")
				continue
			}
			err := cmd.fn()
			cmd.resultCh <- err
			if err == nil {
				deleted = true
				close(closeCh)
			}
		case instanceCmdOther:
			if deleted {
				cmd.resultCh <- errors.New("Instance does not exist")
				continue
			}

			/* Instance loop is only interested in errors from create and delete */

			_ = cmd.fn()
		}
	}

	fmt.Println("Instance loop quiting")

	wg.Done()
}

func (s *service) startInstanceLoop(name string) chan instanceCmd {
	instanceCh := make(chan instanceCmd)
	closeCh := make(chan struct{})
	s.instances[name] = instanceCh
	s.instanceChMap[closeCh] = instanceCh
	s.instanceWg.Add(1)
	go instanceLoop(instanceCh, closeCh, &s.instanceWg)
	s.cases = append(s.cases, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(closeCh),
	})
	return instanceCh
}

func (s *service) findExistingInstances() {
	instancesDir := filepath.Join(s.ccvmDir, "instances")

	_ = filepath.Walk(instancesDir, func(path string, info os.FileInfo, err error) error {
		if path == instancesDir {
			return nil
		}

		if !info.IsDir() {
			return nil
		}

		_ = s.startInstanceLoop(info.Name())

		return filepath.SkipDir
	})
}

func (s *service) create(ctx context.Context, resultCh chan interface{}, args *types.CreateArgs) {
	if _, ok := s.instances["instance"]; ok {
		resultCh <- errors.New("Instance already exists")
		return
	}

	args.Name = "instance"

	instanceCh := s.startInstanceLoop("instance")
	instanceCh <- instanceCmd{
		cmdType:  instanceCmdCreate,
		resultCh: resultCh,
		fn: func() error {
			return Create(ctx, resultCh, s.downloadCh, args)
		},
	}
}

func (s *service) stop(ctx context.Context, resultCh chan interface{}) {
	instanceCh, ok := s.instances["instance"]
	if !ok {
		resultCh <- errors.New("Instance does not exist")
		return
	}

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- Stop(ctx, "instance")
			return nil
		},
	}
}

func (s *service) start(ctx context.Context, resultCh chan interface{}, vmSpec *types.VMSpec) {
	instanceCh, ok := s.instances["instance"]
	if !ok {
		resultCh <- errors.New("Instance does not exist")
		return
	}

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- Start(ctx, "instance", vmSpec)
			return nil
		},
	}
}

func (s *service) quit(ctx context.Context, resultCh chan interface{}) {
	instanceCh, ok := s.instances["instance"]
	if !ok {
		resultCh <- errors.New("Instance does not exist")
		return
	}

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- Quit(ctx, "instance")
			return nil
		},
	}
}

func (s *service) delete(ctx context.Context, resultCh chan interface{}) {
	instanceCh, ok := s.instances["instance"]
	if !ok {
		resultCh <- errors.New("Instance does not exist")
		return
	}

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdDelete,
		resultCh: resultCh,
		fn: func() error {
			return Delete(ctx, "instance")
		},
	}
}

func (s *service) status(ctx context.Context, resultCh chan interface{}) {
	instanceCh, ok := s.instances["instance"]
	if !ok {
		resultCh <- errors.New("Instance does not exist")
		return
	}

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			details, err := Status(ctx, "instance")
			if err != nil {
				resultCh <- err
			} else {
				resultCh <- *details
			}
			return nil
		},
	}
}

func (s *service) processAction(action interface{}) {
	switch a := action.(type) {
	case startAction:
		/*
			TODO: Assuming systemd will not send us any new commands
			after it has asked us to shut down.
		*/
		resultCh := make(chan interface{}, 256)
		ctx, cancel := context.WithCancel(context.Background())

		s.transactions[s.counter] = transaction{
			ctx:      ctx,
			cancel:   cancel,
			resultCh: resultCh,
		}
		if s.shutdownTimer != nil {
			if !s.shutdownTimer.Stop() {
				_ = <-s.shutdownTimer.C
			}
			s.shutdownTimer = nil
			s.cases[TimeChIndex].Chan = reflect.ValueOf(nil)
		}
		a.transCh <- s.counter
		s.counter++
		a.action(ctx, s, resultCh)
	case cancelAction:
		fmt.Fprintf(os.Stderr, "Cancelling %d\n", int(a))
		t, ok := s.transactions[int(a)]
		if ok {
			t.cancel()
		}
	case getResult:
		t, ok := s.transactions[int(a.ID)]
		if !ok {
			a.res <- errors.Errorf("Unknown transaction %d", a.ID)
		} else {
			a.res <- t.resultCh
		}
	case completeAction:
		fmt.Printf("Completing %d\n", int(a))
		_, ok := s.transactions[int(a)]
		if !ok {
			panic("Action %d does not exist")
		}
		delete(s.transactions, int(a))
		if len(s.transactions) == 0 {
			if s.shutdownTimer == nil {
				shutdownIn := time.Minute
				if s.shuttingDown {
					shutdownIn = time.Second * 0
				}
				s.shutdownTimer = time.NewTimer(shutdownIn)
				s.cases[TimeChIndex].Chan = reflect.ValueOf(s.shutdownTimer.C)
			}
		}
	}
}

func (s *service) run(doneCh chan struct{}, actionCh chan interface{}) {
	fmt.Println("Starting Service")

	s.transactions = make(map[int]transaction)
	s.cases = []reflect.SelectCase{
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(doneCh),
		},
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(actionCh),
		},
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(nil),
		},
	}

	s.findExistingInstances()

DONE:
	for {
		index, value, _ := reflect.Select(s.cases)
		switch index {
		case DoneChIndex:
			fmt.Printf("Signal received active transactions = %d\n", len(s.transactions))
			if s.shutdownTimer != nil {
				if !s.shutdownTimer.Stop() {
					_ = <-s.shutdownTimer.C
				}
			}
			if len(s.transactions) == 0 {
				break DONE
			}
			s.shuttingDown = true
			for _, t := range s.transactions {
				t.cancel()
			}
			s.cases[DoneChIndex].Chan = reflect.ValueOf(nil)
		case ActionChIndex:
			s.processAction(value.Interface())
		case TimeChIndex:
			break DONE
		default:
			/* One of the instanceLoops has quit */

			fmt.Println("Instance loop has died")
			closeCh := s.cases[index].Chan.Interface().(chan struct{})
			close(s.instanceChMap[closeCh])
			delete(s.instances, "instance")
			delete(s.instanceChMap, closeCh)
			s.cases = append(s.cases[:index], s.cases[index+1:]...)
		}
	}

	for _, instanceCh := range s.instances {
		close(instanceCh)
	}
	s.instanceWg.Wait()

	fmt.Println("Shutting down Service")
}

func makeDir() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not defined")
	}
	ccvmDir := filepath.Join(home, ".ccloudvm")
	err := os.MkdirAll(ccvmDir, 0700)
	if err != nil {
		return "", errors.Wrapf(err, "Unable to create %s", ccvmDir)
	}

	return ccvmDir, nil
}

func getListener(domainParent string) (net.Listener, error) {
	if systemd {
		listeners, err := activation.Listeners(true)
		if err != nil {
			return nil, errors.Wrap(err, "Unable to retrieve systemd listeners")
		}

		if len(listeners) != 1 {
			return nil, errors.New("Expected only 1 socket activation fd")
		}

		return listeners[0], nil
	}

	listener, err := net.Listen("unix", filepath.Join(domainParent, "socket"))
	if err != nil {
		return nil, errors.Wrap(err, "Unable to create listener")
	}
	return listener, nil
}

func startServer(signalCh chan os.Signal) error {
	ccvmDir, err := makeDir()
	if err != nil {
		return err
	}
	listener, err := getListener(ccvmDir)
	if err != nil {
		return err
	}

	defer func() {
		_ = listener.Close()
	}()

	api := &ServerAPI{
		signalCh: signalCh,
		actionCh: make(chan interface{}),
	}
	err = rpc.Register(api)
	if err != nil {
		return errors.New("Unable to register RPC API")
	}
	rpc.HandleHTTP()

	ccvmServer := &http.Server{}
	finishedCh := make(chan struct{})
	doneCh := make(chan struct{})

	fmt.Println("Running server")

	var wg sync.WaitGroup

	downloadCh := make(chan downloadRequest)
	d := downloader{}
	err = d.setup(ccvmDir)
	if err != nil {
		return errors.Wrap(err, "Unable to start download manager")
	}

	wg.Add(1)
	go func() {
		d.start(doneCh, downloadCh)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		_ = ccvmServer.Serve(listener)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		svc := &service{
			ccvmDir:       ccvmDir,
			downloadCh:    downloadCh,
			instances:     make(map[string]chan instanceCmd),
			instanceChMap: make(map[chan struct{}]chan instanceCmd),
		}
		svc.run(doneCh, api.actionCh)
		close(finishedCh)
		wg.Done()
	}()
	select {
	case <-signalCh:
		fmt.Println("Signal channel closed")
		close(doneCh)
	case <-finishedCh:
		close(doneCh)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	err = ccvmServer.Shutdown(ctx)
	cancel()
	wg.Wait()
	if err != nil {
		return errors.Wrap(err, "ccloudvm server did not shut down correctly")
	}

	return nil
}

func main() {
	fmt.Println("Starting")

	flag.Parse()

	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	err := startServer(signalCh)
	fmt.Println("Quiting")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
