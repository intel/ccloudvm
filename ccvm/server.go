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
	"regexp"
	"sort"
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
var hostnameRegexp *regexp.Regexp

func init() {
	flag.BoolVar(&systemd, "systemd", true, "Use systemd socket activation if true")
	hostnameRegexp = regexp.MustCompile("^[A-Za-z0-9\\-]+$")
}

type service interface {
	create(context.Context, chan interface{}, *types.CreateArgs)
	stop(context.Context, string, chan interface{})
	start(context.Context, string, *types.VMSpec, chan interface{})
	quit(context.Context, string, chan interface{})
	delete(context.Context, string, chan interface{})
	status(context.Context, string, chan interface{})
	getInstances(context.Context, chan interface{})
}

type startAction struct {
	action  func(ctx context.Context, s service, resultCh chan interface{})
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

type ccvmService struct {
	ccvmDir       string
	downloadCh    chan<- downloadRequest
	counter       int
	shutdownTimer *time.Timer
	transactions  map[int]transaction
	shuttingDown  bool
	cases         []reflect.SelectCase
	hostIPs       map[uint32]struct{}
	instances     map[string]chan instanceCmd
	hostIPMask    uint32
	instanceChMap map[chan struct{}]string
	instanceWg    sync.WaitGroup
	b             backend
}

func returnCreateResult(createCmd instanceCmd, name string, err error) {
	if err != nil {
		createCmd.resultCh <- err
	} else {
		createCmd.resultCh <- types.CreateResult{
			Name:     name,
			Finished: true,
		}
	}
}

func instanceLoop(name string, instanceCh <-chan instanceCmd, closeCh chan struct{}, wg *sync.WaitGroup) {
	var createCh chan error
	var createCmd instanceCmd

	deleted := false

DONE:
	for {
		select {
		case cmd, ok := <-instanceCh:
			if !ok {
				break DONE
			}
			switch cmd.cmdType {
			case instanceCmdCreate:
				if deleted {
					cmd.resultCh <- errors.New("Instance already exists (but is being deleted)")
					continue
				}
				createCh = make(chan error)
				createCmd = cmd
				go func() {
					createCh <- cmd.fn()
				}()
			default:
				if deleted {
					cmd.resultCh <- errors.New("Instance does not exist")
					continue
				}
				if createCh != nil {
					cmd.resultCh <- errors.New("Command not allowed while instance is being created")
					continue
				}
				if cmd.cmdType == instanceCmdDelete {
					err := cmd.fn()
					cmd.resultCh <- err
					if err == nil {
						deleted = true
						close(closeCh)
					}
				} else {
					/* Instance loop is only interested in errors from create and delete */
					_ = cmd.fn()
				}
			}
		case err := <-createCh:
			createCh = nil
			returnCreateResult(createCmd, name, err)
			if err != nil {
				deleted = true
				close(closeCh)
			}
		}
	}

	if createCh != nil {
		err := <-createCh
		returnCreateResult(createCmd, name, err)
	}

	fmt.Println("Instance loop quiting")

	wg.Done()
}

func flattenIP(ip net.IP) (uint32, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, errors.Errorf("%s is not a valid IP4 address", ip)
	}

	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3]), nil
}

func (s *ccvmService) startInstanceLoop(name string, flatIP uint32) chan instanceCmd {
	instanceCh := make(chan instanceCmd)
	closeCh := make(chan struct{})
	s.instances[name] = instanceCh
	s.instanceChMap[closeCh] = name
	s.hostIPs[flatIP] = struct{}{}
	s.instanceWg.Add(1)
	go instanceLoop(name, instanceCh, closeCh, &s.instanceWg)
	s.cases = append(s.cases, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(closeCh),
	})
	return instanceCh
}

func (s *ccvmService) findFreeIP() (net.IP, uint32, error) {
	i := s.hostIPMask + 1
	maxIPs := i + 254
	for ; i < maxIPs; i++ {
		if _, ok := s.hostIPs[uint32(i)]; !ok {
			break
		}
	}

	if i == maxIPs {
		return net.IP{}, 0, errors.New("No IP addresses left")
	}

	a := byte((0xff000000 & i) >> 24)
	b := byte((0xff0000 & i) >> 16)
	c := byte((0xff00 & i) >> 8)
	d := byte(0xff & i)

	return net.IPv4(a, b, c, d), uint32(i), nil
}

func (s *ccvmService) findExistingInstances() {
	instancesDir := filepath.Join(s.ccvmDir, "instances")

	_ = filepath.Walk(instancesDir, func(path string, info os.FileInfo, err error) error {
		if path == instancesDir {
			return nil
		}

		if !info.IsDir() {
			return nil
		}

		details, err := s.b.status(context.Background(), info.Name())
		if err != nil {
			fmt.Printf("Unable to read state information for %s\n", info.Name())
			return filepath.SkipDir
		}

		flatIP, err := flattenIP(details.VMSpec.HostIP)
		if err != nil {
			fmt.Printf("Unable to parse IP address %s\n", details.VMSpec.HostIP)
			return filepath.SkipDir
		}

		fmt.Printf("Starting instance %s on %s\n", info.Name(), details.VMSpec.HostIP)

		_ = s.startInstanceLoop(info.Name(), flatIP)

		return filepath.SkipDir
	})
}

func (s *ccvmService) getInstance(instanceName string) (string, error) {
	if instanceName == "" {
		if len(s.instances) == 0 {
			return "", errors.New("Instance does not exist")
		}
		if len(s.instances) > 1 {
			return "", errors.New("Please specify an instance name")
		}
		for k := range s.instances {
			return k, nil
		}
	}

	if _, ok := s.instances[instanceName]; !ok {
		return "", errors.New("Instance does not exist")
	}

	return instanceName, nil
}

func (s *ccvmService) newInstanceName() (string, error) {
	var name string
	var i int

	for i := 0; i < maxNames(); i++ {
		name = makeRandomName()
		if _, ok := s.instances[name]; !ok {
			break
		}
	}

	if i == maxNames() {
		return "", errors.New("All names are used up")
	}

	return name, nil
}

func (s *ccvmService) create(ctx context.Context, resultCh chan interface{}, args *types.CreateArgs) {
	if args.Name == "" {
		instanceName, err := s.newInstanceName()
		if err != nil {
			resultCh <- err
			return
		}
		args.Name = instanceName
	} else {
		if !hostnameRegexp.MatchString(args.Name) {
			resultCh <- errors.Errorf("Invalid hostname %s", args.Name)
			return
		}

		if _, ok := s.instances[args.Name]; ok {
			resultCh <- errors.New("Instance already exists")
			return
		}
	}

	var flatIP uint32
	if len(args.CustomSpec.HostIP) == 0 {
		hostIP, i, err := s.findFreeIP()
		if err != nil {
			resultCh <- err
			return
		}
		args.CustomSpec.HostIP = hostIP
		flatIP = i
	} else {
		flatIP, err := flattenIP(args.CustomSpec.HostIP)
		if err != nil {
			resultCh <- err
			return
		}
		_, ok := s.hostIPs[flatIP]
		if ok {
			resultCh <- errors.Errorf("IP address %s is already in use", args.CustomSpec.HostIP)
			return
		}
	}

	instanceCh := s.startInstanceLoop(args.Name, flatIP)
	instanceCh <- instanceCmd{
		cmdType:  instanceCmdCreate,
		resultCh: resultCh,
		fn: func() error {
			return s.b.createInstance(ctx, resultCh, s.downloadCh, args)
		},
	}
}

func (s *ccvmService) stop(ctx context.Context, instanceName string, resultCh chan interface{}) {
	instanceName, err := s.getInstance(instanceName)
	if err != nil {
		resultCh <- err
		return
	}
	instanceCh := s.instances[instanceName]

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- s.b.stop(ctx, instanceName)
			return nil
		},
	}
}

func (s *ccvmService) start(ctx context.Context, instanceName string, vmSpec *types.VMSpec, resultCh chan interface{}) {
	instanceName, err := s.getInstance(instanceName)
	if err != nil {
		resultCh <- err
		return
	}
	instanceCh := s.instances[instanceName]

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- s.b.start(ctx, instanceName, vmSpec)
			return nil
		},
	}
}

func (s *ccvmService) quit(ctx context.Context, instanceName string, resultCh chan interface{}) {
	instanceName, err := s.getInstance(instanceName)
	if err != nil {
		resultCh <- err
		return
	}
	instanceCh := s.instances[instanceName]

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			resultCh <- s.b.quit(ctx, instanceName)
			return nil
		},
	}
}

func (s *ccvmService) delete(ctx context.Context, instanceName string, resultCh chan interface{}) {
	instanceName, err := s.getInstance(instanceName)
	if err != nil {
		resultCh <- err
		return
	}
	instanceCh := s.instances[instanceName]

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdDelete,
		resultCh: resultCh,
		fn: func() error {
			return s.b.deleteInstance(ctx, instanceName)
		},
	}
}

func (s *ccvmService) status(ctx context.Context, instanceName string, resultCh chan interface{}) {
	instanceName, err := s.getInstance(instanceName)
	if err != nil {
		resultCh <- err
		return
	}
	instanceCh := s.instances[instanceName]

	instanceCh <- instanceCmd{
		cmdType:  instanceCmdOther,
		resultCh: resultCh,
		fn: func() error {
			details, err := s.b.status(ctx, instanceName)
			if err != nil {
				resultCh <- err
			} else {
				resultCh <- *details
			}
			return nil
		},
	}
}

func (s *ccvmService) getInstances(ctx context.Context, resultCh chan interface{}) {
	names := make([]string, len(s.instances))
	i := 0
	for k := range s.instances {
		names[i] = k
		i++
	}
	sort.Strings(names)
	resultCh <- names
}

func (s *ccvmService) processAction(action interface{}) {
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

func (s *ccvmService) run(doneCh chan struct{}, actionCh chan interface{}) {
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
			name := s.instanceChMap[closeCh]
			close(s.instances[name])
			delete(s.instances, name)
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
		svc := &ccvmService{
			ccvmDir:       ccvmDir,
			downloadCh:    downloadCh,
			instances:     make(map[string]chan instanceCmd),
			instanceChMap: make(map[chan struct{}]string),
			hostIPs:       make(map[uint32]struct{}),
			hostIPMask:    0x7f000000 | uint32((os.Getuid()&0xffff)<<8),
			b:             ccvmBackend{},
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
