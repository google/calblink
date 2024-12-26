// Copyright 2017 Google Inc.
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

package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"time"
)

// flags
var debugFlag = flag.Bool("debug", false, "Show debug messages")
var clientSecretFlag = flag.String("clientsecret", "client_secret.json", "Path to JSON file containing client secret")
var calNameFlag = flag.String("calendar", "primary", "Name of calendar to base blinker on (overrides value in config file)")
var configFileFlag = flag.String("config", "conf.json", "Path to configuration file")
var pollIntervalFlag = flag.Int("poll_interval", 30, "Number of seconds between polls of calendar API (overrides value in config file)")
var responseStateFlag = flag.String("response_state", "notRejected", "Which events to consider based on response: all, accepted, or notRejected")
var deviceFailureRetriesFlag = flag.Int("device_failure_retries", 10, "Number of times to retry initializing the device before quitting the program")
var showDotsFlag = flag.Bool("show_dots", true, "Whether to show progress dots after every cycle of checking the calendar")

var debugOut io.Writer = ioutil.Discard
var dotOut io.Writer = ioutil.Discard

// Time calculation methods

func tomorrow() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

func setHourMinuteFromTime(t time.Time) time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
}

func sleep(d time.Duration) {
	// To fix the 'oversleeping' problem where we sleep too long if the machine goes to
	// sleep in the meantime, sleep for no more than 5 minutes at once.
	// TODO: Once the AbsoluteNow proposal goes in, replace this with that.
	max := time.Duration(5) * time.Minute
	if d > max {
		fmt.Fprintf(debugOut, "Cutting sleep short from %d to %d", d, max)
		d = max
	}
	time.Sleep(d)
}

// Print output methods

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *debugFlag {
		debugOut = os.Stdout
	}
	
	userPrefs := readUserPrefs()

	// Overrides from command-line
	flag.Visit(func(myFlag *flag.Flag) {
		switch myFlag.Name {
		case "calendar":
			userPrefs.Calendars = []string{myFlag.Value.String()}
		case "poll_interval":
			userPrefs.PollInterval = myFlag.Value.(flag.Getter).Get().(int)
		case "response_state":
			userPrefs.ResponseState = ResponseState(myFlag.Value.String())
			if !userPrefs.ResponseState.isValidState() {
				log.Fatalf("Invalid response state %v", userPrefs.ResponseState)
			}
		case "device_failure_retries":
			userPrefs.DeviceFailureRetries = myFlag.Value.(flag.Getter).Get().(int)
		case "show_dots":
			userPrefs.ShowDots = myFlag.Value.(flag.Getter).Get().(bool)
		}
	})

	if userPrefs.ShowDots {
		dotOut = os.Stdout
	}

	srv, err := Connect()
	if err != nil {
		log.Fatalf("Unable to retrieve Calendar client: %v", err)
	}

	blinkerState := NewBlinkerState(userPrefs.DeviceFailureRetries)

	go signalHandler(blinkerState)
	go blinkerState.patternRunner()

	printStartInfo(userPrefs)

	failures := 0

	for {
		now := time.Now()
		weekday := now.Weekday()
		if userPrefs.SkipDays[weekday] {
			tomorrow := tomorrow()
			untilTomorrow := tomorrow.Sub(now)
			Black.Execute(blinkerState)
			fmt.Fprintf(debugOut, "Sleeping %v until tomorrow because it's a skip day\n", untilTomorrow)
			fmt.Fprint(dotOut, "~")
			sleep(untilTomorrow)
			continue
		}
		if userPrefs.StartTime != nil {
			start := setHourMinuteFromTime(*userPrefs.StartTime)
			fmt.Fprintf(debugOut, "Start time: %v\n", start)
			if diff := time.Since(start); diff < 0 {
				Black.Execute(blinkerState)
				fmt.Fprintf(debugOut, "Sleeping %v because start time after now\n", -diff)
				fmt.Fprint(dotOut, ">")
				sleep(-diff)
				continue
			}
		}
		if userPrefs.EndTime != nil {
			end := setHourMinuteFromTime(*userPrefs.EndTime)
			fmt.Fprintf(debugOut, "End time: %v\n", end)
			if diff := time.Since(end); diff > 0 {
				Black.Execute(blinkerState)
				tomorrow := tomorrow()
				untilTomorrow := tomorrow.Sub(now)
				fmt.Fprintf(debugOut, "Sleeping %v until tomorrow because end time %v before now\n", untilTomorrow, diff)
				fmt.Fprint(dotOut, "<")
				sleep(untilTomorrow)
				continue
			}
		}
		next, err := fetchEvents(now, srv, userPrefs)
		if err != nil {
			// Leave the same color, set a flag. If we get more than a critical number of these,
			// set the color to blinking magenta to tell the user we are in a failed state.
			failures++
			if failures > failureRetries {
				MagentaFlash.Execute(blinkerState)
			}
			fmt.Fprintf(debugOut, "Fetch Error:\n%v\n", err)
			fmt.Fprint(dotOut, ",")
			sleep(time.Duration(userPrefs.PollInterval) * time.Second)
			continue
		} else {
			failures = 0
		}
		blinkState := blinkStateForEvent(next, userPrefs.PriorityFlashSide)

		blinkState.Execute(blinkerState)
		fmt.Fprint(dotOut, ".")
		sleep(time.Duration(userPrefs.PollInterval) * time.Second)
	}
	

}
