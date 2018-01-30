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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/build"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"text/template"
	"time"

	"github.com/intel/ccloudvm/types"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

const ccloudvmPkg = "github.com/intel/ccloudvm"

var indentedRegexp *regexp.Regexp

func init() {
	indentedRegexp = regexp.MustCompile("\\s+.*")
}

type workload struct {
	spec           workloadSpec
	userData       string
	parent         *workload
	mergedUserData []byte
}

func (wkld *workload) save(ws *workspace) error {
	var buf bytes.Buffer

	_, _ = buf.WriteString("---\n")
	data, err := yaml.Marshal(wkld.spec)
	if err != nil {
		return errors.Wrap(err, "Unable to marshal instance specification")
	}
	_, _ = buf.Write(data)
	_, _ = buf.WriteString("...\n")

	_, _ = buf.WriteString("---\n")
	_, _ = buf.Write(wkld.mergedUserData)
	_, _ = buf.WriteString("...\n")

	err = ioutil.WriteFile(path.Join(ws.instanceDir, "state.yaml"),
		buf.Bytes(), 0600)
	if err != nil {
		return errors.Wrap(err, "Unable to write instance state")
	}

	return nil
}

func (spec *workloadSpec) ensureSSHPortMapping() {
	var i int
	for i = 0; i < len(spec.VM.PortMappings); i++ {
		if spec.VM.PortMappings[i].Guest == 22 {
			break
		}
	}

	if i == len(spec.VM.PortMappings) {
		spec.VM.PortMappings = append(spec.VM.PortMappings,
			types.PortMapping{
				Host:  10022,
				Guest: 22,
			})
	}
}

func workloadFromURL(ctx context.Context, u url.URL) ([]byte, error) {
	var workloadPath string

	switch u.Scheme {
	case "http", "https":
		workloadFile, err := ioutil.TempFile("", ".workload")
		if err != nil {
			return nil, errors.Wrap(err, "Failed to create a temporal file")
		}

		workloadPath = workloadFile.Name()
		defer func() { _ = os.Remove(workloadPath) }()

		// 60 seconds should be enough to download the workload file
		ctx, cancelFunc := context.WithTimeout(ctx, 60*time.Second)
		err = getFile(ctx, u.String(), workloadFile, downloadProgress)
		cancelFunc()
		if err != nil {
			return nil, errors.Wrapf(err, "Unable download workload file from %s", u.String())
		}
	case "file":
		workloadPath = u.Path

	default:
		return nil, fmt.Errorf("Unable download workload file %s: unsupported scheme", u.String())
	}

	return ioutil.ReadFile(workloadPath)
}

func loadWorkloadData(ctx context.Context, ws *workspace, workloadName string) ([]byte, error) {
	u, err := url.Parse(workloadName)
	if err != nil {
		return nil, errors.Wrapf(err, "Unable to parse workload name %s", workloadName)
	}

	// Absolute means that it has a non-empty scheme
	if u.IsAbs() {
		return workloadFromURL(ctx, *u)
	}

	wkld, err := ioutil.ReadFile(workloadName)
	if err == nil {
		return wkld, nil
	}

	localPath := filepath.Join(ws.ccvmDir, "workloads", fmt.Sprintf("%s.yaml", workloadName))
	wkld, err = ioutil.ReadFile(localPath)
	if err == nil {
		return wkld, nil
	}

	p, err := build.Default.Import(ccloudvmPkg, "", build.FindOnly)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to locate ccloudvm workload directory")
	}
	workloadPath := filepath.Join(p.Dir, "workloads", fmt.Sprintf("%s.yaml", workloadName))
	wkld, err = ioutil.ReadFile(workloadPath)
	if err != nil {
		return nil, fmt.Errorf("Unable to load workload %s", workloadPath)
	}

	return wkld, nil
}

func unmarshalWorkload(ws *workspace, wkld *workload, spec,
	userData string) error {
	err := wkld.spec.unmarshalWithTemplate(ws, spec)
	if err != nil {
		return err
	}

	wkld.userData = userData

	return nil
}

func (wkld *workload) merge(parent *workload) {
	if wkld.spec.BaseImageURL == "" {
		wkld.spec.BaseImageURL = parent.spec.BaseImageURL
	}

	if wkld.spec.BaseImageName == "" {
		wkld.spec.BaseImageName = parent.spec.BaseImageName
	}

	if wkld.spec.Hostname == "" {
		wkld.spec.Hostname = parent.spec.Hostname
	}

	// Always better to require nested VM that not.
	if !wkld.spec.NeedsNestedVM {
		wkld.spec.NeedsNestedVM = parent.spec.NeedsNestedVM
	}

	wkld.spec.VM.Merge(&parent.spec.VM)
}

type cloudConfig map[string]interface{}

func (cc cloudConfig) merge(p cloudConfig) error {
	for k, v := range p {
		// copy from parents to child if not present
		if _, ok := cc[k]; !ok {
			cc[k] = v
			continue
		}

		// else merge slices and maps
		switch reflect.ValueOf(v).Type().Kind() {
		case reflect.Slice:
			if reflect.ValueOf(cc[k]).Type() != reflect.ValueOf(v).Type() {
				return fmt.Errorf("Types of merged sequences not equal for: %s", k)
			}

			// create new slice appending current slice (cc[k]) onto the parent slice (v)
			s := reflect.AppendSlice(reflect.ValueOf(v), reflect.ValueOf(cc[k]))

			// update the top-level map (cc) for the current key (k) with the temporary slice (s)
			reflect.ValueOf(cc).SetMapIndex(reflect.ValueOf(k), s)
		case reflect.Map:

			// first level merging only
			if reflect.ValueOf(cc[k]).Type() != reflect.ValueOf(v).Type() {
				return fmt.Errorf("Types of merged maps not equal for: %s", k)
			}

			// for each key (kk) in the parent map (v)...
			for _, kk := range reflect.ValueOf(v).MapKeys() {
				// ... check if the key (kk) is present in the current map (cc[k])
				if !reflect.ValueOf(cc[k]).MapIndex(kk).IsValid() {
					// ... and if not copy into the current map (cc[k])
					reflect.ValueOf(cc[k]).SetMapIndex(kk, reflect.ValueOf(v).MapIndex(kk))
				}
			}
		}

		// and for everything else just use version in cc
	}
	return nil
}

func (wkld *workload) parse(ws *workspace) (cloudConfig, error) {
	var p cloudConfig
	var err error
	if wkld.parent != nil {
		p, err = wkld.parent.parse(ws)
		if err != nil {
			return nil, err
		}
	}

	funcMap := template.FuncMap{
		"proxyVars":    proxyVarsFN,
		"proxyEnv":     proxyEnvFN,
		"download":     downloadFN,
		"beginTask":    beginTaskFN,
		"endTaskCheck": endTaskCheckFN,
		"endTaskOk":    endTaskOkFN,
		"endTaskFail":  endTaskFailFN,
	}

	udt, err := template.New("user-data").Funcs(funcMap).Parse(wkld.userData)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to parse user data template")
	}

	var udBuf bytes.Buffer
	err = udt.Execute(&udBuf, ws)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to execute user data template")
	}

	cc := make(cloudConfig)
	err = yaml.Unmarshal(udBuf.Bytes(), cc)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to unmarshal userdata")
	}

	// remove empty top levels from the earlier parse
	for k, v := range cc {
		if v == nil {
			delete(cc, k)
		}
	}

	if p != nil {
		err = cc.merge(p)
		if err != nil {
			return nil, errors.Wrap(err, "Error merging cloud-config data")
		}
	}

	return cc, err
}

func (wkld *workload) generateCloudConfig(ws *workspace) error {
	data, err := wkld.parse(ws)
	if err != nil {
		return errors.Wrap(err, "Error parsing workload")
	}

	finishedStr := fmt.Sprintf(`curl -X PUT -d "FINISHED" 10.0.2.2:%d`,
		ws.HTTPServerPort)
	if v, ok := data["runcmd"]; ok {
		runcmds := v.([]interface{})
		runcmds = append(runcmds, finishedStr)
		data["runcmd"] = runcmds
	} else {
		data["runcmd"] = []string{finishedStr}
	}

	output, err := yaml.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "Error marshalling cloud-config")
	}

	wkld.mergedUserData = append([]byte("#cloud-config\n"), output...)

	return nil
}

func createWorkload(ctx context.Context, ws *workspace, workloadName string) (*workload, error) {
	data, err := loadWorkloadData(ctx, ws, workloadName)
	if err != nil {
		return nil, err
	}

	var wkld workload
	var spec, userData string
	docs := splitYaml(data)
	if len(docs) == 2 {
		spec = string(docs[0])
		userData = string(docs[1])
	} else {
		return nil, errors.New("Invalid workload; two documents required")
	}

	err = unmarshalWorkload(ws, &wkld, spec, userData)
	if err != nil {
		return nil, err
	}
	if wkld.spec.WorkloadName == "" {
		wkld.spec.WorkloadName = workloadName
	}

	if wkld.spec.Inherits != "" {
		wkld.parent, err = createWorkload(ctx, ws, wkld.spec.Inherits)
		if err != nil {
			return nil, errors.Wrapf(err, "Error loading inherited workload: %s", wkld.spec.Inherits)
		}
		wkld.merge(wkld.parent)
	} else {
		wkld.merge(defaultWorkload())
	}

	wkld.spec.ensureSSHPortMapping()

	return &wkld, nil
}

func restoreWorkload(ws *workspace) (*workload, error) {
	var wkld workload
	data, err := ioutil.ReadFile(path.Join(ws.instanceDir, "state.yaml"))
	if err != nil {
		return nil, errors.Wrap(err, "Error reading workload")
	}

	docs := splitYaml(data)
	if len(docs) != 2 {
		return nil, errors.New("Invalid workload; must have two documents")

	}

	err = unmarshalWorkload(ws, &wkld, string(docs[0]), string(docs[1]))
	return &wkld, err
}

func findDocument(lines [][]byte) ([]byte, int) {
	var realStart int
	var realEnd int
	docStartFound := false
	docEndFound := false

	start := len(lines) - 1
	line := lines[start]
	if bytes.HasPrefix(line, []byte("...")) {
		docEndFound = true
		realEnd = start
		start--
	}

	for ; start >= 0; start-- {
		line := lines[start]
		if bytes.HasPrefix(line, []byte("---")) {
			docStartFound = true
			break
		}
		if bytes.HasPrefix(line, []byte("...")) {
			start++
			break
		}
	}

	if docStartFound {
		realStart = start + 1
		for start = start - 1; start >= 0; start-- {
			line := lines[start]
			if !bytes.HasPrefix(line, []byte{'%'}) {
				break
			}
		}
		start++
	} else {
		if start < 0 {
			start = 0
		}
		realStart = start
	}

	if !docEndFound {
		realEnd = len(lines)
	}

	var buf bytes.Buffer
	for _, line := range lines[realStart:realEnd] {
		_, _ = buf.Write(line)
		_ = buf.WriteByte('\n')
	}

	return buf.Bytes(), start
}

func splitYaml(data []byte) [][]byte {
	lines := make([][]byte, 0, 256)
	docs := make([][]byte, 0, 3)

	reader := bytes.NewReader(data)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		lineC := make([]byte, len(line))
		_ = copy(lineC, line)
		lines = append(lines, lineC)
	}

	endOfNextDoc := len(lines)
	for endOfNextDoc > 0 {
		var doc []byte
		doc, endOfNextDoc = findDocument(lines[:endOfNextDoc])
		docs = append([][]byte{doc}, docs...)
	}

	return docs
}
