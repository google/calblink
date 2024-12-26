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

// This file manages retrieving and filtering events from Google Calendar.

package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
)

// Event handling methods
func eventHasAcceptableResponse(item *calendar.Event, responseState ResponseState) bool {
	for _, attendee := range item.Attendees {
		if attendee.Self {
			return responseState.CheckStatus(attendee.ResponseStatus)
		}
	}
	fmt.Fprintf(debugOut, "No self attendee found for %v\n", item)
	fmt.Fprintf(debugOut, "Attendees: %v\n", item.Attendees)
	return true
}

func eventExcludedByPrefs(item string, userPrefs *UserPrefs) bool {
	if userPrefs.Excludes[item] {
		return true
	}
	for _, prefix := range userPrefs.ExcludePrefixes {
		if strings.HasPrefix(item, prefix) {
			fmt.Fprintf(debugOut, "Skipping event '%v' due to prefix match '%v'\n", item, prefix)
			return true
		}
	}
	return false
}

func nextEvent(items []*calendar.Event, locations []WorkSite, userPrefs *UserPrefs) []*calendar.Event {
	var events []*calendar.Event

	if len(userPrefs.WorkingLocations) > 0 {
		match := false
		locationSet := make(map[WorkSite]bool)
		for _, location := range locations {
			locationSet[location] = true
		}

		for _, prefLocation := range userPrefs.WorkingLocations {
			if locationSet[prefLocation] {
				fmt.Fprintf(debugOut, "Found matching location: %v\n", prefLocation)
				match = true
				break
			}
		}

		if !match {
			fmt.Fprintf(debugOut, "Skipping all events due to no matching locations in %v\n", locations)
			return events
		}
	}

	for _, i := range items {
		if i.Start.DateTime != "" &&
			!eventExcludedByPrefs(i.Summary, userPrefs) &&
			eventHasAcceptableResponse(i, userPrefs.ResponseState) {
			events = append(events, i)
			if len(events) == 2 || (len(events) == 1 && !userPrefs.MultiEvent) {
				break
			}
		}
	}
	fmt.Fprintf(debugOut, "nextEvent returning %d events\n", len(events))
	return events
}

func blinkStateForDelta(delta float64) CalendarState {
	blinkState := Black
	switch {
	case delta < -1:
		blinkState = Blue
	case delta < 0:
		blinkState = BlueFlash
	case delta < 2:
		blinkState = FastRedFlash
	case delta < 5:
		blinkState = RedFlash
	case delta < 10:
		blinkState = Red
	case delta < 30:
		blinkState = Yellow
	case delta < 60:
		blinkState = Green
	}
	return blinkState
}

func blinkStateForEvent(next []*calendar.Event, priority int) CalendarState {
	blinkState := Black
	for i, event := range next {
		startTime, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err == nil {
			delta := -time.Since(startTime).Minutes()
			if i == 0 {
				blinkState = blinkStateForDelta(delta)
			} else {
				secondary := blinkStateForDelta(delta)
				if secondary != Black {
					blinkState = CombineStates(blinkState, secondary)
				}
				if (priority == 1 && blinkState.primaryFlash == 0 && blinkState.secondaryFlash > 0) ||
					(priority == 2 && blinkState.primaryFlash > 0 && blinkState.secondaryFlash == 0) {
					fmt.Fprintf(debugOut, "Swapping")
					blinkState = SwapState(blinkState)
				}
			}
			fmt.Fprintf(debugOut, "Event %v, time %v, delta %v, state %v\n", event.Summary, startTime, delta, blinkState.Name)
			// Set priority.  If priority is set, and the other light is flashing but the priority one isn't, swap them.

		} else {
			fmt.Println(err)
			break
		}
	}
	return blinkState
}

func fetchEvents(now time.Time, srv *calendar.Service, userPrefs *UserPrefs) ([]*calendar.Event, error) {
	start := now.Format(time.RFC3339)
	endTime := now.Add(2 * time.Hour)
	end := endTime.Format(time.RFC3339)
	var allEvents []*calendar.Event
	locations := make([]WorkSite, 0)
	for _, calendar := range userPrefs.Calendars {
		var locationCreated time.Time
		var location WorkSite
		events, err := srv.Events.List(calendar).ShowDeleted(false).
			SingleEvents(true).TimeMin(start).TimeMax(end).OrderBy("startTime").
			EventTypes("default", "focusTime", "outOfOffice", "workingLocation").Do()
		if err != nil {
			return nil, err
		}
		for _, event := range events.Items {
			if event.EventType == "workingLocation" {
				// There's a bug in the Calendar API where a recurring location that is
				// overridden for the day still shows up in the list of events.  The most
				// recently created one is the one we want.
				thisCreated, err := time.Parse(time.RFC3339, event.Created)
				if err != nil || thisCreated.Before(locationCreated) {
					continue
				}
				locationProperties := event.WorkingLocationProperties
				locationType := makeWorkSiteType(locationProperties.Type)
				locationString := ""
				switch locationType {
				case WorkSiteOffice:
					locationString = locationProperties.OfficeLocation.Label
				case WorkSiteCustom:
					locationString = locationProperties.CustomLocation.Label
				}
				location = WorkSite{SiteType: locationType, Name: locationString}
				locationCreated = thisCreated
				fmt.Fprintf(debugOut, "Location detected: calendar %v, location %v\n", calendar, location)
			}
		}
		if !locationCreated.IsZero() {
			fmt.Fprintf(debugOut, "Adding final location %v\n", location)
			locations = append(locations, location)
		}
		allEvents = append(allEvents, events.Items...)
	}
	if len(userPrefs.Calendars) > 1 {
		// Filter out copies of the same event, or ones with times that don't parse.
		var filtered []*calendar.Event
		seen := make(map[string]bool)
		for _, event := range allEvents {
			if seen[event.Id] {
				fmt.Fprintf(debugOut, "Skipping duplicate event with ID %v\n", event.Id)
				continue
			}
			if event.Start.DateTime == "" {
				fmt.Fprintf(debugOut, "Skipping all-day event %v\n", event.Summary)
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
	return nextEvent(allEvents, locations, userPrefs), nil
}
