// Copyright 2024 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This file manages running calblink as a service.

package main

import (
	"log"
	"os"

	"github.com/kardianos/service"
)

func (p *program) StartService(serviceCmd string) {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	svcConfig := &service.Config{
		Name:             "calblink",
		DisplayName:      "calblink",
		Description:      "Service to monitor Google Calendar to control a blink(1)",
		Arguments:        []string{"-runAsService"},
		WorkingDirectory: dir,
		Option: service.KeyValue{
			"UserService": true,
		},
	}
	s, err := service.New(p, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	p.service = s
	if len(serviceCmd) != 0 {
		err := service.Control(s, serviceCmd)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}
	err = s.Run()
	if err != nil {
		log.Fatal(err)
	}
}

func (p *program) Start(s service.Service) error {
	go runLoop(p)
	return nil
}

func (p *program) Stop(s service.Service) error {
	close(p.exit)
	return nil
}
