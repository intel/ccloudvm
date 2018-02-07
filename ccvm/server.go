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

type service struct {
	downloadCh    chan<- downloadRequest
	counter       int
	shutdownTimer *time.Timer
	transactions  map[int]transaction
	shuttingDown  bool
	cases         []reflect.SelectCase
}

func (s *service) create(ctx context.Context, resultCh chan interface{}, args *types.CreateArgs) {
	resultCh <- Create(ctx, resultCh, s.downloadCh, args)
}

func (s *service) stop(ctx context.Context, resultCh chan interface{}) {
	resultCh <- Stop(ctx)
}

func (s *service) start(ctx context.Context, resultCh chan interface{}, vmSpec *types.VMSpec) {
	resultCh <- Start(ctx, vmSpec)
}

func (s *service) quit(ctx context.Context, resultCh chan interface{}) {
	resultCh <- Quit(ctx)
}

func (s *service) delete(ctx context.Context, resultCh chan interface{}) {
	resultCh <- Delete(ctx)
}

func (s *service) status(ctx context.Context, resultCh chan interface{}) {
	details, err := Status(ctx)
	result := getInstanceComplete{
		err: err,
	}
	if err == nil {
		result.details = *details
	}
	resultCh <- result
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
		go a.action(ctx, s, resultCh)
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
	s.transactions = make(map[int]transaction)

	fmt.Println("Starting Service")

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
		}
	}

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
			downloadCh: downloadCh,
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
