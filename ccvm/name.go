/*
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
*/

package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

var randomAdjectives = []string{
	"alarmed",
	"amused",
	"angry",
	"astonished",
	"bored",
	"concerned",
	"considerate",
	"depressed",
	"distracted",
	"glad",
	"gloomy",
	"happy",
	"incensed",
	"sad",
	"serene",
	"sleepy",
	"tense",
	"thoughtful",
	"tired",
	"vague",
	"worried",
}

var randomNames = []string{
	"agravain",
	"arthur",
	"balin",
	"balan",
	"bedivere",
	"bors",
	"dagonet",
	"elaine",
	"gaheris",
	"galahad",
	"gareth",
	"gawain",
	"glatistant",
	"guinevere",
	"hector",
	"igraine",
	"isolde",
	"kay",
	"lancelot",
	"leodegrance",
	"lot",
	"lynette",
	"malegant",
	"margawse",
	"mark",
	"merlin",
	"mordred",
	"morgan",
	"nimue",
	"owain",
	"palomides",
	"parcival",
	"peles",
	"pelinor",
	"tristram",
	"uther",
}

type nameIndex struct {
	n int
	a int
}

var nameGenerator = struct {
	sync.Mutex
	rand     *rand.Rand
	counter  int
	indicies []nameIndex
}{}

func init() {
	nameGenerator.rand = rand.New(rand.NewSource(time.Now().Unix()))
	nameGenerator.indicies = make([]nameIndex, maxNames())
	for i := range randomAdjectives {
		for j := range randomNames {
			nameGenerator.indicies[(i*len(randomNames))+j] = nameIndex{j, i}
		}
	}
	for i := 0; i < len(nameGenerator.indicies); i++ {
		r1 := nameGenerator.rand.Int() % len(nameGenerator.indicies)
		r2 := nameGenerator.rand.Int() % len(nameGenerator.indicies)
		nameGenerator.indicies[r1], nameGenerator.indicies[r2] = nameGenerator.indicies[r2], nameGenerator.indicies[r1]
	}
}

func maxNames() int {
	return len(randomAdjectives) * len(randomNames)
}

func makeRandomName() string {
	nameGenerator.Lock()
	ni := nameGenerator.indicies[nameGenerator.counter]
	nameGenerator.counter++
	if nameGenerator.counter == maxNames() {
		nameGenerator.counter = 0
	}
	nameGenerator.Unlock()
	return fmt.Sprintf("%s_%s", randomAdjectives[ni.a], randomNames[ni.n])
}
