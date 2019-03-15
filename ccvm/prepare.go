//
// Copyright (c) 2016 Intel Corporation
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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"

	"github.com/ciao-project/ciao/uuid"
	"github.com/intel/ccloudvm/types"
	"github.com/intel/govmm/qemu"
	"github.com/pkg/errors"
)

const metaDataTemplate = `
{
  "uuid": "{{.UUID}}",
  "hostname": "{{.Hostname}}"
}
`
const msgprefix = "MESSAGE:"

type workspace struct {
	GoPath         string
	Home           string
	HTTPProxy      string
	HTTPSProxy     string
	NoProxy        string
	User           string
	UID            int
	GID            int
	PublicKey      string
	HTTPServerPort int
	GitUserName    string
	GitEmail       string
	Mounts         []types.Mount
	Hostname       string
	HostIP         string
	UUID           string
	PackageUpgrade string
	ccvmDir        string
	instanceDir    string
	keyPath        string
	publicKeyPath  string
	dnsSearch      []string
}

func (w *workspace) MountPath(tag string) string {
	for _, m := range w.Mounts {
		if m.Tag == tag {
			return m.Path
		}
	}

	return ""
}

func hostSupportsNestedKVMIntel() bool {
	data, err := ioutil.ReadFile("/sys/module/kvm_intel/parameters/nested")
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(data)) == "Y"
}

func hostSupportsNestedKVMAMD() bool {
	data, err := ioutil.ReadFile("/sys/module/kvm_amd/parameters/nested")
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(data)) == "1"
}

func hostSupportsNestedKVM() bool {
	return hostSupportsNestedKVMIntel() || hostSupportsNestedKVMAMD()
}

func prepareSSHKeys(ctx context.Context, ws *workspace) error {
	_, privKeyErr := os.Stat(ws.keyPath)
	_, pubKeyErr := os.Stat(ws.publicKeyPath)

	if pubKeyErr != nil || privKeyErr != nil {
		out, err := exec.CommandContext(ctx, "ssh-keygen",
			"-f", ws.keyPath, "-t", "rsa", "-N", "").CombinedOutput()
		if err != nil {
			return errors.Wrapf(err, "Unable to generate SSH key pair: %s", string(out))
		}
	}

	publicKey, err := ioutil.ReadFile(ws.publicKeyPath)
	if err != nil {
		return errors.Wrap(err, "Unable to read public ssh key")
	}

	ws.PublicKey = string(publicKey)
	return nil
}

func dnsSearch() []string {
	searches := []string{}
	data, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewBuffer(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if fields[0] != "search" {
			continue
		}
		searches = append(searches, fields[1])
	}
	return searches
}

func prepareEnv(ctx context.Context, name string) (*workspace, error) {
	var err error

	ws := &workspace{}
	ws.Home = os.Getenv("HOME")
	if ws.Home == "" {
		return nil, fmt.Errorf("HOME is not defined")
	}
	ws.User = os.Getenv("USER")
	if ws.User == "" {
		return nil, fmt.Errorf("USER is not defined")
	}

	ws.UID = os.Getuid()
	ws.GID = os.Getgid()

	ws.ccvmDir = path.Join(ws.Home, ".ccloudvm")
	ws.instanceDir = path.Join(ws.ccvmDir, "instances", name)
	ws.keyPath = path.Join(ws.ccvmDir, "id_rsa")
	ws.publicKeyPath = fmt.Sprintf("%s.pub", ws.keyPath)

	data, err := exec.Command("git", "config", "--global", "user.name").Output()
	if err == nil {
		ws.GitUserName = strings.TrimSpace(string(data))
	}

	data, err = exec.Command("git", "config", "--global", "user.email").Output()
	if err == nil {
		ws.GitEmail = strings.TrimSpace(string(data))
	}

	ws.UUID = uuid.Generate().String()
	ws.dnsSearch = dnsSearch()

	return ws, nil
}

func createCloudInitISO(ctx context.Context, instanceDir string, userData, metaData []byte) error {
	isoPath := path.Join(instanceDir, "config.iso")
	return qemu.CreateCloudInitISO(ctx, instanceDir, isoPath, userData, metaData, nil)
}

func downloadFN(ws *workspace, URL, location string) string {
	url := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("10.0.2.2:%d", ws.HTTPServerPort),
		Path:   "download",
	}
	q := url.Query()
	q.Set(urlParam, URL)
	url.RawQuery = q.Encode()
	return fmt.Sprintf("wget %s -O %s", url.String(), location)
}

func beginTaskFN(ws *workspace, message string) string {
	const infoStr = `'curl -X PUT -d "%s" 10.0.2.2:%d'`
	message = strings.Replace(message, "'", "''", -1)
	return fmt.Sprintf(infoStr, message, ws.HTTPServerPort)
}

func endTaskCheckFN(ws *workspace) string {
	const checkStr = `if [ $? -eq 0 ] ; then ret="OK" ; else ret="FAIL" ; fi ; ` +
		`curl -X PUT -d $ret 10.0.2.2:%d`
	return fmt.Sprintf(checkStr, ws.HTTPServerPort)
}

func endTaskOkFN(ws *workspace) string {
	const okStr = `curl -X PUT -d "OK" 10.0.2.2:%d`
	return fmt.Sprintf(okStr, ws.HTTPServerPort)
}

func endTaskFailFN(ws *workspace) string {
	const failStr = `curl -X PUT -d "FAIL" 10.0.2.2:%d`
	return fmt.Sprintf(failStr, ws.HTTPServerPort)
}

func messageFN(ws *workspace, message string) string {
	const msgStr = `'curl -X PUT -d "%s%s" 10.0.2.2:%d'`
	message = strings.Replace(message, "'", "''", -1)
	return fmt.Sprintf(msgStr, msgprefix, message, ws.HTTPServerPort)
}

func proxyVarsFN(ws *workspace) string {
	var buf bytes.Buffer
	if ws.NoProxy != "" {
		buf.WriteString("no_proxy=")
		buf.WriteString(ws.NoProxy)
		buf.WriteString(" ")
		buf.WriteString("NO_PROXY=")
		buf.WriteString(ws.NoProxy)
		buf.WriteString(" ")
	}
	if ws.HTTPProxy != "" {
		buf.WriteString("http_proxy=")
		buf.WriteString(ws.HTTPProxy)
		buf.WriteString(" HTTP_PROXY=")
		buf.WriteString(ws.HTTPProxy)
		buf.WriteString(" ")
	}
	if ws.HTTPSProxy != "" {
		buf.WriteString("https_proxy=")
		buf.WriteString(ws.HTTPSProxy)
		buf.WriteString(" HTTPS_PROXY=")
		buf.WriteString(ws.HTTPSProxy)
		buf.WriteString(" ")
	}
	return strings.TrimSpace(buf.String())
}

func proxyEnvFN(ws *workspace, indent int) string {
	var buf bytes.Buffer
	spaces := strings.Repeat(" ", indent)
	if ws.NoProxy != "" {
		buf.WriteString(spaces)
		buf.WriteString(`no_proxy="`)
		buf.WriteString(ws.NoProxy)
		buf.WriteString(`"` + "\n")
		buf.WriteString(spaces)
		buf.WriteString(`NO_PROXY="`)
		buf.WriteString(ws.NoProxy)
		buf.WriteString(`"` + "\n")
	}
	if ws.HTTPProxy != "" {
		buf.WriteString(spaces)
		buf.WriteString(`http_proxy="`)
		buf.WriteString(ws.HTTPProxy)
		buf.WriteString(`"` + "\n")
		buf.WriteString(spaces)
		buf.WriteString(`HTTP_PROXY="`)
		buf.WriteString(ws.HTTPProxy)
		buf.WriteString(`"` + "\n")
	}
	if ws.HTTPSProxy != "" {
		buf.WriteString(spaces)
		buf.WriteString(`https_proxy="`)
		buf.WriteString(ws.HTTPSProxy)
		buf.WriteString(`"` + "\n")
		buf.WriteString(spaces)
		buf.WriteString(`HTTPS_PROXY="`)
		buf.WriteString(ws.HTTPSProxy)
		buf.WriteString(`"`)
	}
	return buf.String()
}

func buildISOImage(ctx context.Context, resultCh chan interface{}, userData []byte, ws *workspace, debug bool) error {
	mdt, err := template.New("meta-data").Parse(metaDataTemplate)
	if err != nil {
		return errors.Wrap(err, "Unable to parse meta data template")
	}

	var mdBuf bytes.Buffer
	err = mdt.Execute(&mdBuf, ws)
	if err != nil {
		return errors.Wrap(err, "Unable to execute meta data template")
	}

	if debug {
		resultCh <- types.CreateResult{
			Line: string(userData),
		}
		resultCh <- types.CreateResult{
			Line: mdBuf.String(),
		}
	}

	return createCloudInitISO(ctx, ws.instanceDir, userData, mdBuf.Bytes())
}

func createRootfs(ctx context.Context, backingImage, instanceDir string, disk int) error {
	vmImage := path.Join(instanceDir, "image.qcow2")
	if _, err := os.Stat(vmImage); err == nil {
		_ = os.Remove(vmImage)
	}
	diskParam := fmt.Sprintf("%dG", disk)
	params := make([]string, 0, 32)
	params = append(params, "create", "-f", "qcow2", "-o", "backing_file="+backingImage,
		vmImage, diskParam)
	return exec.CommandContext(ctx, "qemu-img", params...).Run()
}
