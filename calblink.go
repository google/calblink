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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	blink1 "github.com/kazrakcom/go-blink1"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

// TODO - make color fade from green to yellow to red
// TODO - add Clock type to manage time-of-day where we currently use Time and hacks to set it to the current day
// Configuration file:
// JSON file with the following structure:
// {
//   excludes: [ "event", "names", "to", "ignore"],
//   excludePrefixes: [ "prefixes", "to", "ignore"],
//   startTime: "hh:mm (24 hr format) to start blinking at every day",
//   endTime: "hh:mm (24 hr format) to stop blinking at every day",
//   skipDays: [ "weekdays", "to", "skip"],
//   pollInterval: 30
//   calendar: "calendar"
//   responseState: "all"
//   deviceFailureRetries: 10
//   showDots: true
//   multiEvent: true
//}
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

// responseState is an enumerated list of event response states, used to control which events will activate the blink(1).
type responseState string

const (
	responseStateAll         = responseState("all")
	responseStateAccepted    = responseState("accepted")
	responseStateNotRejected = responseState("notRejected")
)

// checkStatus returns true if the given event status is one that should activate the blink(1) in the given responseState.
func (state responseState) checkStatus(status string) bool {
	switch state {
	case responseStateAll:
		return true

	case responseStateAccepted:
		return (status == "accepted")

	case responseStateNotRejected:
		return (status != "declined")
	}
	return false
}

func (state responseState) isValidState() bool {
	switch state {
	case responseStateAll:
		return true
	case responseStateAccepted:
		return true
	case responseStateNotRejected:
		return true
	}
	return false
}

// userPrefs is a struct that manages the user preferences as set by the config file and command line.

type userPrefs struct {
	excludes             map[string]bool
	excludePrefixes      []string
	startTime            *time.Time
	endTime              *time.Time
	skipDays             [7]bool
	pollInterval         int
	calendars            []string
	responseState        responseState
	deviceFailureRetries int
	showDots             bool
	multiEvent           bool
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
}

// calendarState is a display state for the calendar event.  It encapsulates both the colors to display and the flash duration.
type calendarState struct {
	name           string
	primary        blink1.State
	secondary      blink1.State
	primaryFlash   time.Duration
	secondaryFlash time.Duration
	alternate      bool
}

func (state calendarState) execute(blinker *blinkerState) {
	blinker.newState <- state
}

var (
	black        = calendarState{name: "Black", primary: blink1.OffState}
	green        = calendarState{name: "Green", primary: blink1.State{Green: 255}, secondary: blink1.State{Green: 255}}
	yellow       = calendarState{name: "Yellow", primary: blink1.State{Red: 255, Green: 160}, secondary: blink1.State{Red: 255, Green: 160}}
	red          = calendarState{name: "Red", primary: blink1.State{Red: 255}, secondary: blink1.State{Red: 255}}
	redFlash     = calendarState{name: "Red Flash", primary: blink1.State{Red: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(500) * time.Millisecond, alternate: true}
	fastRedFlash = calendarState{name: "Fast Red Flash", primary: blink1.State{Red: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(125) * time.Millisecond, alternate: true}
	blueFlash    = calendarState{name: "Red/Blue Flash", primary: blink1.State{Blue: 255}, secondary: blink1.State{Red: 255}, primaryFlash: time.Duration(500) * time.Millisecond, alternate: true}
	blue         = calendarState{name: "Blue", primary: blink1.State{Blue: 255}, secondary: blink1.State{Blue: 255}}
	magentaFlash = calendarState{name: "MagentaFlash", primary: blink1.State{Red: 255, Blue: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(125) * time.Millisecond, alternate: true}
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

const failureRetries = 3

// blinkerState encapsulates the current device state of the blink(1).
type blinkerState struct {
	device      *blink1.Device
	newState    chan calendarState
	failures    int
	maxFailures int
}

func newBlinkerState(maxFailures int) *blinkerState {
	blinker := &blinkerState{
		newState:    make(chan calendarState, 1),
		maxFailures: maxFailures,
	}
	blinker.reinitialize()
	return blinker
}

func (blinker *blinkerState) reinitialize() error {
	if blinker.device != nil {
		blinker.device.Close()
		blinker.device = nil
	}
	device, err := blink1.OpenNextDevice()
	if err != nil {
		blinker.failures++
		if blinker.failures > blinker.maxFailures {
			log.Fatalf("Unable to initialize blink(1): %v", err)
		}
		fmt.Fprint(dotOut, "X")
	} else {
		blinker.failures = 0
	}
	blinker.device = device
	return err
}

func (blinker *blinkerState) setState(state blink1.State) error {
	if blinker.failures > 0 {
		err := blinker.reinitialize()
		if err != nil {
			fmt.Fprintf(debugOut, "Reinitialize failed, error %v\n", err)
			return err
		}
	}
	err := blinker.device.SetState(state)
	if err != nil {
		fmt.Fprintf(debugOut, "Re-initializing because of error %v\n", err)
		err = blinker.reinitialize()
		if err != nil {
			fmt.Fprintf(debugOut, "Reinitialize failed, error %v\n", err)
			return err
		}
		// Try one more time before giving up for this pass.
		err = blinker.device.SetState(state)
		if err != nil {
			fmt.Fprintf(debugOut, "Setting blinker state failed, error %v\n", err)
		}
	} else {
		blinker.failures = 0
	}
	return err
}

func (blinker *blinkerState) patternRunner() {
	currentState := black
	failing := false
	err := blinker.setState(currentState.primary)
	if err != nil {
		failing = true
	}

	var ticker <-chan time.Time
	stateFlip := false
	for {
		select {
		case newState := <-blinker.newState:
			if newState != currentState || failing {
				fmt.Fprintf(debugOut, "Changing from state %v to %v\n", currentState, newState)
				currentState = newState
				if newState.primaryFlash > 0 || newState.secondaryFlash > 0 {
					ticker = time.After(time.Millisecond)
				} else {
					if ticker != nil {
						fmt.Fprintf(debugOut, "Killing timer\n")
						ticker = nil
					}
					state1 := newState.primary
					state1.LED = blink1.LED1
					state2 := newState.secondary
					state2.LED = blink1.LED2
					err1 := blinker.setState(state1)
					err2 := blinker.setState(state2)
					failing = (err1 != nil) || (err2 != nil)
				}
			} else {
				fmt.Fprintf(debugOut, "Retaining state %v unchanged\n", newState)
			}

		case <-ticker:
			fmt.Fprintf(debugOut, "Timer fired\n")
			state1 := currentState.primary
			state2 := currentState.secondary
			if stateFlip {
				if currentState.alternate {
				state1, state2 = state2, state1
				} else {
					if currentState.primaryFlash > 0 {
					    state1 = blink1.OffState
					}
					if currentState.secondaryFlash > 0 {
					    state2 = blink1.OffState
					}
				}
			}
			state1.Duration = currentState.primaryFlash
			state1.FadeTime = state1.Duration
			if currentState.alternate {
					state2.Duration, state2.FadeTime = state1.Duration, state1.FadeTime

			} else {
			state2.Duration = currentState.secondaryFlash
			state2.FadeTime = state2.Duration
			}
			// We set state1 on LED 1 and state2 on LED 2.  On an original (mk1) blink(1) state2 will be ignored.
			state1.LED = blink1.LED1
			state2.LED = blink1.LED2
			fmt.Fprintf(debugOut, "Setting state (%v and %v)\n", state1, state2)
			err1 := blinker.setState(state1)
			err2 := blinker.setState(state2)
			failing = (err1 != nil) || (err2 != nil)
			stateFlip = !stateFlip
			ticker = time.After(state1.Duration)
		}
	}
}

// Signal handler - SIGINT or SIGKILL should turn off the blinker before we exit.
// SIGQUIT should turn on debug mode.

func signalHandler(blinker *blinkerState) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill, syscall.SIGQUIT)
	for {
		s := <-interrupt
		if s == syscall.SIGQUIT {
			fmt.Println("Turning on debug mode.\n")
			debugOut = os.Stdout
			continue
		}
		if blinker.failures == 0 {
			blinker.newState <- black
			blinker.device.SetState(blink1.OffState)
		}
		log.Fatalf("Quitting due to signal %v", s)
	}
}

// BEGIN GOOGLE CALENDAR API SAMPLE CODE

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func getClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile, err := tokenCacheFile()
	if err != nil {
		log.Fatalf("Unable to get path to cached credential file. %v", err)
	}
	tok, err := tokenFromFile(cacheFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok)
}

// getTokenFromWeb uses Config to request a Token.
// It returns the retrieved Token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}

// tokenCacheFile generates credential file path/filename.
// It returns the generated credential path/filename.
func tokenCacheFile() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	tokenCacheDir := filepath.Join(usr.HomeDir, ".credentials")
	os.MkdirAll(tokenCacheDir, 0700)
	return filepath.Join(tokenCacheDir,
		url.QueryEscape("calendar-blink1.json")), err
}

// tokenFromFile retrieves a Token from a given file path.
// It returns the retrieved Token and any read error encountered.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.Create(file)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

// END GOOGLE CALENDAR API SAMPLE CODE

// Event handling methods
func eventHasAcceptableResponse(item *calendar.Event, responseState responseState) bool {
	for _, attendee := range item.Attendees {
		if attendee.Self {
			return responseState.checkStatus(attendee.ResponseStatus)
		}
	}
	fmt.Fprintf(debugOut, "No self attendee found for %v\n", item)
	fmt.Fprintf(debugOut, "Attendees: %v\n", item.Attendees)
	return true
}

func eventExcludedByPrefs(item string, userPrefs *userPrefs) bool {
	if userPrefs.excludes[item] {
		return true
	}
	for _, prefix := range userPrefs.excludePrefixes {
		if strings.HasPrefix(item, prefix) {
			fmt.Fprintf(debugOut, "Skipping event '%v' due to prefix match '%v'\n", item, prefix)
			return true
		}
	}
	return false
}

func nextEvent(items []*calendar.Event, userPrefs *userPrefs) []*calendar.Event {
	var events []*calendar.Event
	for _, i := range items {
		if i.Start.DateTime != "" &&
			!eventExcludedByPrefs(i.Summary, userPrefs) &&
			eventHasAcceptableResponse(i, userPrefs.responseState) {
			events = append(events, i)
			if len(events) == 2 || (len(events) == 1 && !userPrefs.multiEvent) {
				break
			}
		}
	}
	fmt.Fprintf(debugOut, "nextEvent returning %d events\n", len(events))
	return events
}

func blinkStateForDelta(delta float64) calendarState {
	blinkState := black
	switch {
	case delta < -1:
		blinkState = blue
	case delta < 0:
		blinkState = blueFlash
	case delta < 2:
		blinkState = fastRedFlash
	case delta < 5:
		blinkState = redFlash
	case delta < 10:
		blinkState = red
	case delta < 30:
		blinkState = yellow
	case delta < 60:
		blinkState = green
	}
	return blinkState
}

func blinkStateForEvent(next []*calendar.Event) calendarState {
	blinkState := black
	for i, event := range next {
		startTime, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err == nil {
			delta := -time.Since(startTime).Minutes()
			if i == 0 {
				blinkState = blinkStateForDelta(delta)
			} else {
				secondary := blinkStateForDelta(delta)
				if secondary != black {
					combined := calendarState{name: blinkState.name + "/" + secondary.name,
						primary:        blinkState.primary,
						secondary:      secondary.primary,
						primaryFlash:   blinkState.primaryFlash,
						secondaryFlash: secondary.primaryFlash,
						alternate:      false}
					blinkState = combined
				}
			}
			fmt.Fprintf(debugOut, "Event %v, time %v, delta %v, state %v\n", event.Summary, startTime, delta, blinkState.name)
		} else {
			fmt.Println(err)
			break
		}
	}
	return blinkState
}

func fetchEvents(now time.Time, srv *calendar.Service, userPrefs *userPrefs) ([]*calendar.Event, error) {
	start := now.Format(time.RFC3339)
	endTime := now.Add(2 * time.Hour)
	end := endTime.Format(time.RFC3339)
	var allEvents []*calendar.Event
	for _, calendar := range userPrefs.calendars {
		events, err := srv.Events.List(calendar).ShowDeleted(false).
			SingleEvents(true).TimeMin(start).TimeMax(end).OrderBy("startTime").Do()
		if err != nil {
			return nil, err
		}
		allEvents = append(allEvents, events.Items...)
	}
	if len(userPrefs.calendars) > 1 {
		// Filter out copies of the same event, or ones with times that don't parse.
		var filtered []*calendar.Event
		seen := make(map[string]bool)
		for _, event := range allEvents {
			if seen[event.Id] {
			fmt.Fprintf(debugOut, "Skipping duplicate event with ID %v\n", event.Id)
				continue
			}
			if event.Start.DateTime == "" {
				fmt.Fprintf(debugOut, "Skipping all-day event %v: %v\n", event.Summary)
				continue
			}
			filtered = append(filtered, event)
			seen[event.Id] = true
		}
		sort.SliceStable(filtered, func(i, j int) bool {
			t1, err1 := time.Parse(time.RFC3339, filtered[i].Start.DateTime)
			t2, err2 := time.Parse(time.RFC3339, filtered[j].Start.DateTime)
			// We should have filtered any bad times out already, so this is a fatal error.
			if err1 != nil {
				log.Fatalf("Found bad time after times should have been filtered out: %v\n", err1)
			}
			if err2 != nil {
				log.Fatalf("Found bad time after times should have been filtered out: %v\n", err2)
			}
			return t1.Before(t2)
		})
		allEvents = filtered
	}
	return nextEvent(allEvents, userPrefs), nil
}

// User preferences methods

func readUserPrefs() *userPrefs {
	userPrefs := &userPrefs{}
	// Set defaults from command line
	userPrefs.pollInterval = *pollIntervalFlag
	userPrefs.calendars = []string{*calNameFlag}
	userPrefs.responseState = responseState(*responseStateFlag)
	userPrefs.deviceFailureRetries = *deviceFailureRetriesFlag
	userPrefs.showDots = *showDotsFlag
	file, err := os.Open(*configFileFlag)
	defer file.Close()
	if err != nil {
		// Lack of a config file is not a fatal error.
		fmt.Fprintf(debugOut, "Unable to read config file %v : %v\n", *configFileFlag, err)
		return userPrefs
	}
	prefs := prefLayout{}
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&prefs)
	fmt.Fprintf(debugOut, "Decoded prefs: %v\n", prefs)
	if err != nil {
		log.Fatalf("Unable to parse config file %v", err)
	}
	if prefs.StartTime != "" {
		startTime, err := time.Parse("15:04", prefs.StartTime)
		if err != nil {
			log.Fatalf("Invalid start time %v : %v", prefs.StartTime, err)
		}
		userPrefs.startTime = &startTime
	}
	if prefs.EndTime != "" {
		endTime, err := time.Parse("15:04", prefs.EndTime)
		if err != nil {
			log.Fatalf("Invalid end time %v : %v", prefs.EndTime, err)
		}
		userPrefs.endTime = &endTime
	}
	userPrefs.excludes = make(map[string]bool)
	for _, item := range prefs.Excludes {
		fmt.Fprintf(debugOut, "Excluding item %v\n", item)
		userPrefs.excludes[item] = true
	}
	userPrefs.excludePrefixes = prefs.ExcludePrefixes
	weekdays := make(map[string]int)
	for i := 0; i < 7; i++ {
		weekdays[time.Weekday(i).String()] = i
	}
	for _, day := range prefs.SkipDays {
		i, ok := weekdays[day]
		if ok {
			userPrefs.skipDays[i] = true
		} else {
			log.Fatalf("Invalid day in skipdays: %v", day)
		}
	}
	if prefs.Calendar != "" {
		userPrefs.calendars = []string{prefs.Calendar}
	}
	if len(prefs.Calendars) > 0 {
		userPrefs.calendars = prefs.Calendars
	}
	if prefs.PollInterval != 0 {
		userPrefs.pollInterval = int(prefs.PollInterval)
	}
	if prefs.ResponseState != "" {
		userPrefs.responseState = responseState(prefs.ResponseState)
		if !userPrefs.responseState.isValidState() {
			log.Fatalf("Invalid response state %v", prefs.ResponseState)
		}
	}
	if prefs.DeviceFailureRetries != 0 {
		userPrefs.deviceFailureRetries = int(prefs.DeviceFailureRetries)
	}
	if prefs.ShowDots != "" {
		userPrefs.showDots = (prefs.ShowDots == "true")
	}
	userPrefs.multiEvent = (prefs.MultiEvent == "true")
	fmt.Fprintf(debugOut, "User prefs: %v\n", userPrefs)
	return userPrefs
}

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

func printStartInfo(userPrefs *userPrefs) {
	fmt.Printf("Running with %v second intervals\n", userPrefs.pollInterval)
	if len(userPrefs.calendars) == 1 {
		fmt.Printf("Monitoring calendar ID %v\n", userPrefs.calendars[0])
	} else {
		fmt.Println("Monitoring calendar IDs:")
		for _, item := range userPrefs.calendars {
			fmt.Printf("   %v\n", item)
		}
	}
	switch userPrefs.responseState {
	case responseStateAll:
		fmt.Println("All events shown, regardless of accepted/rejected status.")
	case responseStateAccepted:
		fmt.Println("Only accepted events shown.")
	case responseStateNotRejected:
		fmt.Println("Rejected events not shown.")
	}
	if len(userPrefs.excludes) > 0 {
		fmt.Println("Excluded events:")
		for item := range userPrefs.excludes {
			fmt.Printf("   %v\n", item)
		}
	}
	if len(userPrefs.excludePrefixes) > 0 {
		fmt.Println("Excluded event prefixes:")
		for _, item := range userPrefs.excludePrefixes {
			fmt.Printf("   %v\n", item)
		}
	}
	skipDays := ""
	join := ""
	for i, val := range userPrefs.skipDays {
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
	if userPrefs.startTime != nil {
		timeString += fmt.Sprintf("Time restrictions: after %02d:%02d", userPrefs.startTime.Hour(), userPrefs.startTime.Minute())
	}
	if userPrefs.endTime != nil {
		endTimeString := fmt.Sprintf("until %02d:%02d", userPrefs.endTime.Hour(), userPrefs.endTime.Minute())
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
	if userPrefs.multiEvent {
		fmt.Println("Multievent is active.")
	}
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
			userPrefs.calendars = []string{myFlag.Value.String()}
		case "poll_interval":
			userPrefs.pollInterval = myFlag.Value.(flag.Getter).Get().(int)
		case "response_state":
			userPrefs.responseState = responseState(myFlag.Value.String())
			if !userPrefs.responseState.isValidState() {
				log.Fatalf("Invalid response state %v", userPrefs.responseState)
			}
		case "device_failure_retries":
			userPrefs.deviceFailureRetries = myFlag.Value.(flag.Getter).Get().(int)
		case "show_dots":
			userPrefs.showDots = myFlag.Value.(flag.Getter).Get().(bool)
		}
	})

	if userPrefs.showDots {
		dotOut = os.Stdout
	}

	// BEGIN GOOGLE CALENDAR API SAMPLE CODE
	ctx := context.Background()

	b, err := ioutil.ReadFile(*clientSecretFlag)
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, calendar.CalendarReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(ctx, config)

	srv, err := calendar.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve calendar Client %v", err)
	}
	// END GOOGLE CALENDAR API SAMPLE CODE

	blinkerState := newBlinkerState(userPrefs.deviceFailureRetries)

	go signalHandler(blinkerState)
	go blinkerState.patternRunner()

	printStartInfo(userPrefs)

	failures := 0

	for {
		now := time.Now()
		weekday := now.Weekday()
		if userPrefs.skipDays[weekday] {
			tomorrow := tomorrow()
			untilTomorrow := tomorrow.Sub(now)
			black.execute(blinkerState)
			fmt.Fprintf(debugOut, "Sleeping %v until tomorrow because it's a skip day\n", untilTomorrow)
			fmt.Fprint(dotOut, "~")
			sleep(untilTomorrow)
			continue
		}
		if userPrefs.startTime != nil {
			start := setHourMinuteFromTime(*userPrefs.startTime)
			fmt.Fprintf(debugOut, "Start time: %v\n", start)
			if diff := time.Since(start); diff < 0 {
				black.execute(blinkerState)
				fmt.Fprintf(debugOut, "Sleeping %v because start time after now\n", -diff)
				fmt.Fprint(dotOut, ">")
				sleep(-diff)
				continue
			}
		}
		if userPrefs.endTime != nil {
			end := setHourMinuteFromTime(*userPrefs.endTime)
			fmt.Fprintf(debugOut, "End time: %v\n", end)
			if diff := time.Since(end); diff > 0 {
				black.execute(blinkerState)
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
				magentaFlash.execute(blinkerState)
			}
			fmt.Fprintf(debugOut, "Fetch Error:\n%v\n", err)
			fmt.Fprint(dotOut, ",")
			sleep(time.Duration(userPrefs.pollInterval) * time.Second)
			continue
		} else {
			failures = 0
		}
		blinkState := blinkStateForEvent(next)

		blinkState.execute(blinkerState)
		fmt.Fprint(dotOut, ".")
		sleep(time.Duration(userPrefs.pollInterval) * time.Second)
	}
}
