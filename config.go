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

// This file manages reading the user configuration file.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Configuration file:
// TOML file with the following structure:
//   excludes = ["event", "names", "to", "ignore"]
//   excludePrefixes = ["prefixes", "to", "ignore"]
//   startTime = "hh:mm (24 hr format) to start blinking at every day"
//   endTime = "hh:mm (24 hr format) to stop blinking at every day"
//   skipDays = [ "weekdays", "to", "skip"]
//   pollInterval = 30
//   calendar = "calendar"
//   responseState = "all"
//   deviceFailureRetries = 10
//   showDots = true
//   multiEvent = true
//   priorityFlashSide = 1
//
// An older JSON format is also supported but you don't want to use it.
//
// Notes on items:
// Calendar is the calendar ID - the email address of the calendar.  For a person's calendar, that's their email.
//   For a secondary calendar, it's the base64 string @group.calendar.google.com on the calendar details page. "primary"
//   is a magic string that means "the logged-in user's primary calendar".
// SkipDays may be localized.
// Excludes is exact string matches only.
// ExcludePrefixes will exclude all events starting with the given prefix.
// ResponseState can be one of: "all" (all events whatever their response status), "accepted" (only accepted events),
// "notRejected" (any events that are not rejected).  Default is notRejected.
// DeviceFailureRetries is the number of consecutive failures to initialize the device before the program quits. Default is 10.
// ShowDots indicates whether to show dots and similar marks to indicate that the program has completed an update cycle.
// MultiEvent indicates whether to show two events if there are multiple events in the time range.
// userPrefs is a struct that manages the user preferences as set by the config file and command line.

type UserPrefs struct {
	Excludes             map[string]bool
	ExcludePrefixes      []string
	StartTime            *time.Time
	EndTime              *time.Time
	SkipDays             [7]bool
	PollInterval         int
	Calendars            []string
	ResponseState        ResponseState
	DeviceFailureRetries int
	ShowDots             bool
	MultiEvent           bool
	PriorityFlashSide    int
	WorkingLocations     []WorkSite
}

// Struct used for decoding the JSON
type prefLayout struct {
	Excludes             []string
	ExcludePrefixes      []string
	StartTime            string
	EndTime              string
	SkipDays             []string
	PollInterval         int64
	Calendar             string
	Calendars            []string
	ResponseState        string
	DeviceFailureRetries int64
	ShowDots             string
	MultiEvent           string
	PriorityFlashSide    int64
	WorkingLocations     []string
}

type tomlLayout struct {
	Excludes             []string
	ExcludePrefixes      []string
	StartTime            string
	EndTime              string
	SkipDays             []string
	PollInterval         int64
	Calendar             string
	Calendars            []string
	ResponseState        string
	DeviceFailureRetries int64
	ShowDots             bool
	MultiEvent           bool
	PriorityFlashSide    int64
	WorkingLocations     []string
}

// responseState is an enumerated list of event response states, used to control which events will activate the blink(1).
type ResponseState string

const (
	ResponseStateAll         = ResponseState("all")
	ResponseStateAccepted    = ResponseState("accepted")
	ResponseStateNotRejected = ResponseState("notRejected")
)

// checkStatus returns true if the given event status is one that should activate the blink(1) in the given responseState.
func (state ResponseState) CheckStatus(status string) bool {
	switch state {
	case ResponseStateAll:
		return true

	case ResponseStateAccepted:
		return (status == "accepted")

	case ResponseStateNotRejected:
		return (status != "declined")
	}
	return false
}

func (state ResponseState) isValidState() bool {
	switch state {
	case ResponseStateAll:
		return true
	case ResponseStateAccepted:
		return true
	case ResponseStateNotRejected:
		return true
	}
	return false
}

// Work site information

type WorkSiteType int

const (
	WorkSiteHome WorkSiteType = iota
	WorkSiteOffice
	WorkSiteCustom
)

func makeWorkSiteType(location string) WorkSiteType {
	switch location {
	case "officeLocation", "office":
		return WorkSiteOffice
	case "customLocation", "custom":
		return WorkSiteCustom
	}
	return WorkSiteHome
}

func (siteType WorkSiteType) toString() string {
	switch siteType {
	case WorkSiteHome:
		return "Home"
	case WorkSiteOffice:
		return "Office"
	case WorkSiteCustom:
		return "Custom"
	}
	return ""
}

// workSite is a struct that holds a working location.  If name is unset, should match
// all sites of the given type.
type WorkSite struct {
	SiteType WorkSiteType
	Name     string
}

// Converts a string into a workSite structure.  Returns an unset structure if the string is invalid.
func makeWorkSite(location string) WorkSite {
	split := strings.SplitN(location, ":", 2)
	siteType := makeWorkSiteType(split[0])
	name := ""
	if len(split) > 1 {
		name = split[1]
	}
	debugLog("Work Site: type %v, name %v", siteType, name)
	return WorkSite{SiteType: siteType, Name: name}
}

// User preferences methods

func readUserPrefs() *UserPrefs {
	configFile := *configFileFlag
	_, err := os.Stat(configFile)
	if errors.Is(err, fs.ErrNotExist) {
		// primary file doesn't exist, try the secondary.
		configFile = *backupConfigFileFlag
	}
	if strings.HasSuffix(configFile, "toml") {
		return readTomlPrefs(configFile)
	} else {
		return readJsonPrefs(configFile)
	}
}

func readTomlPrefs(configFile string) *UserPrefs {
	prefs := tomlLayout{}
	userPrefs := &UserPrefs{}
	// Set defaults from command line
	userPrefs.PollInterval = *pollIntervalFlag
	userPrefs.Calendars = []string{*calNameFlag}
	userPrefs.ResponseState = ResponseState(*responseStateFlag)
	userPrefs.DeviceFailureRetries = *deviceFailureRetriesFlag
	userPrefs.ShowDots = *showDotsFlag
	_, err := toml.DecodeFile(configFile, &prefs)
	debugLog("Decoded TOML: %v\n", prefs)
	if err != nil {
		log.Fatalf("Unable to parse config file %v", err)
	}
	if prefs.StartTime != "" {
		startTime, err := time.Parse("15:04", prefs.StartTime)
		if err != nil {
			log.Fatalf("Invalid start time %v : %v", prefs.StartTime, err)
		}
		userPrefs.StartTime = &startTime
	}
	if prefs.EndTime != "" {
		endTime, err := time.Parse("15:04", prefs.EndTime)
		if err != nil {
			log.Fatalf("Invalid end time %v : %v", prefs.EndTime, err)
		}
		userPrefs.EndTime = &endTime
	}
	userPrefs.Excludes = make(map[string]bool)
	for _, item := range prefs.Excludes {
		debugLog("Excluding item %v\n", item)
		userPrefs.Excludes[item] = true
	}
	userPrefs.ExcludePrefixes = prefs.ExcludePrefixes
	weekdays := make(map[string]int)
	for i := 0; i < 7; i++ {
		weekdays[time.Weekday(i).String()] = i
	}
	for _, day := range prefs.SkipDays {
		i, ok := weekdays[day]
		if ok {
			userPrefs.SkipDays[i] = true
		} else {
			log.Fatalf("Invalid day in skipdays: %v", day)
		}
	}
	if prefs.Calendar != "" {
		userPrefs.Calendars = []string{prefs.Calendar}
	}
	if len(prefs.Calendars) > 0 {
		userPrefs.Calendars = prefs.Calendars
	}
	if prefs.PollInterval != 0 {
		userPrefs.PollInterval = int(prefs.PollInterval)
	}
	if prefs.ResponseState != "" {
		userPrefs.ResponseState = ResponseState(prefs.ResponseState)
		if !userPrefs.ResponseState.isValidState() {
			log.Fatalf("Invalid response state %v", prefs.ResponseState)
		}
	}
	if prefs.DeviceFailureRetries != 0 {
		userPrefs.DeviceFailureRetries = int(prefs.DeviceFailureRetries)
	}
	userPrefs.ShowDots = prefs.ShowDots
	userPrefs.MultiEvent = prefs.MultiEvent
	if prefs.PriorityFlashSide != 0 {
		userPrefs.PriorityFlashSide = int(prefs.PriorityFlashSide)
	}
	for _, location := range prefs.WorkingLocations {
		userPrefs.WorkingLocations = append(userPrefs.WorkingLocations, makeWorkSite(location))
	}
	debugLog("User prefs: %v\n", userPrefs)
	return userPrefs
}

func readJsonPrefs(configFile string) *UserPrefs {
	userPrefs := &UserPrefs{}
	// Set defaults from command line
	userPrefs.PollInterval = *pollIntervalFlag
	userPrefs.Calendars = []string{*calNameFlag}
	userPrefs.ResponseState = ResponseState(*responseStateFlag)
	userPrefs.DeviceFailureRetries = *deviceFailureRetriesFlag
	userPrefs.ShowDots = *showDotsFlag
	file, err := os.Open(configFile)
	defer file.Close()
	if err != nil {
		// Lack of a config file is not a fatal error.
		debugLog("Unable to read config file %v : %v\n", *configFileFlag, err)
		return userPrefs
	}
	prefs := prefLayout{}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&prefs)
	debugLog("Decoded prefs: %v\n", prefs)
	if err != nil {
		log.Fatalf("Unable to parse config file %v", err)
	}
	if prefs.StartTime != "" {
		startTime, err := time.Parse("15:04", prefs.StartTime)
		if err != nil {
			log.Fatalf("Invalid start time %v : %v", prefs.StartTime, err)
		}
		userPrefs.StartTime = &startTime
	}
	if prefs.EndTime != "" {
		endTime, err := time.Parse("15:04", prefs.EndTime)
		if err != nil {
			log.Fatalf("Invalid end time %v : %v", prefs.EndTime, err)
		}
		userPrefs.EndTime = &endTime
	}
	userPrefs.Excludes = make(map[string]bool)
	for _, item := range prefs.Excludes {
		debugLog("Excluding item %v\n", item)
		userPrefs.Excludes[item] = true
	}
	userPrefs.ExcludePrefixes = prefs.ExcludePrefixes
	weekdays := make(map[string]int)
	for i := 0; i < 7; i++ {
		weekdays[time.Weekday(i).String()] = i
	}
	for _, day := range prefs.SkipDays {
		i, ok := weekdays[day]
		if ok {
			userPrefs.SkipDays[i] = true
		} else {
			log.Fatalf("Invalid day in skipdays: %v", day)
		}
	}
	if prefs.Calendar != "" {
		userPrefs.Calendars = []string{prefs.Calendar}
	}
	if len(prefs.Calendars) > 0 {
		userPrefs.Calendars = prefs.Calendars
	}
	if prefs.PollInterval != 0 {
		userPrefs.PollInterval = int(prefs.PollInterval)
	}
	if prefs.ResponseState != "" {
		userPrefs.ResponseState = ResponseState(prefs.ResponseState)
		if !userPrefs.ResponseState.isValidState() {
			log.Fatalf("Invalid response state %v", prefs.ResponseState)
		}
	}
	if prefs.DeviceFailureRetries != 0 {
		userPrefs.DeviceFailureRetries = int(prefs.DeviceFailureRetries)
	}
	if prefs.ShowDots != "" {
		userPrefs.ShowDots = (prefs.ShowDots == "true")
	}
	userPrefs.MultiEvent = (prefs.MultiEvent == "true")
	if prefs.PriorityFlashSide != 0 {
		userPrefs.PriorityFlashSide = int(prefs.PriorityFlashSide)
	}
	for _, location := range prefs.WorkingLocations {
		userPrefs.WorkingLocations = append(userPrefs.WorkingLocations, makeWorkSite(location))
	}
	debugLog("User prefs: %v\n", userPrefs)
	return userPrefs
}

func printStartInfo(userPrefs *UserPrefs) {
	fmt.Printf("Running with %v second intervals\n", userPrefs.PollInterval)
	if len(userPrefs.Calendars) == 1 {
		fmt.Printf("Monitoring calendar ID %v\n", userPrefs.Calendars[0])
	} else {
		fmt.Println("Monitoring calendar IDs:")
		for _, item := range userPrefs.Calendars {
			fmt.Printf("   %v\n", item)
		}
	}
	switch userPrefs.ResponseState {
	case ResponseStateAll:
		fmt.Println("All events shown, regardless of accepted/rejected status.")
	case ResponseStateAccepted:
		fmt.Println("Only accepted events shown.")
	case ResponseStateNotRejected:
		fmt.Println("Rejected events not shown.")
	}
	if len(userPrefs.WorkingLocations) > 0 {
		fmt.Println("Working Locations:")
		for _, item := range userPrefs.WorkingLocations {
			if item.SiteType == WorkSiteHome {
				fmt.Printf("   Home\n")
			} else {
				fmt.Printf("   %v: %v\n", item.SiteType.toString(), item.Name)
			}
		}
	}
	if len(userPrefs.Excludes) > 0 {
		fmt.Println("Excluded events:")
		for item := range userPrefs.Excludes {
			fmt.Printf("   %v\n", item)
		}
	}
	if len(userPrefs.ExcludePrefixes) > 0 {
		fmt.Println("Excluded event prefixes:")
		for _, item := range userPrefs.ExcludePrefixes {
			fmt.Printf("   %v\n", item)
		}
	}
	skipDays := ""
	join := ""
	for i, val := range userPrefs.SkipDays {
		if val {
			skipDays += join
			skipDays += time.Weekday(i).String()
			join = ", "
		}
	}
	if len(skipDays) > 0 {
		fmt.Println("Skip days: " + skipDays)
	}
	timeString := ""
	if userPrefs.StartTime != nil {
		timeString += fmt.Sprintf("Time restrictions: after %02d:%02d", userPrefs.StartTime.Hour(), userPrefs.StartTime.Minute())
	}
	if userPrefs.EndTime != nil {
		endTimeString := fmt.Sprintf("until %02d:%02d", userPrefs.EndTime.Hour(), userPrefs.EndTime.Minute())
		if len(timeString) > 0 {
			timeString += " and "
		} else {
			timeString += "Time restrictions: "
		}
		timeString += endTimeString
	}
	if len(timeString) > 0 {
		fmt.Println(timeString)
	}
	if userPrefs.MultiEvent {
		fmt.Println("Multievent is active.")
	}
}
