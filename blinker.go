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

// This file manages the blink(1) state.

package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	blink1 "github.com/kazrakcom/go-blink1"
)

const failureRetries = 3

// calendarState is a display state for the calendar event.  It encapsulates both the colors to display and the flash duration.
type CalendarState struct {
	Name           string
	primary        blink1.State
	secondary      blink1.State
	primaryFlash   time.Duration
	secondaryFlash time.Duration
	alternate      bool
}

func (state CalendarState) Execute(blinker *BlinkerState) {
	blinker.newState <- state
}

var (
	Black        = CalendarState{Name: "Black", primary: blink1.OffState}
	Green        = CalendarState{Name: "Green", primary: blink1.State{Green: 255}, secondary: blink1.State{Green: 255}}
	Yellow       = CalendarState{Name: "Yellow", primary: blink1.State{Red: 255, Green: 160}, secondary: blink1.State{Red: 255, Green: 160}}
	Red          = CalendarState{Name: "Red", primary: blink1.State{Red: 255}, secondary: blink1.State{Red: 255}}
	RedFlash     = CalendarState{Name: "Red Flash", primary: blink1.State{Red: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(500) * time.Millisecond, alternate: true}
	FastRedFlash = CalendarState{Name: "Fast Red Flash", primary: blink1.State{Red: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(125) * time.Millisecond, alternate: true}
	BlueFlash    = CalendarState{Name: "Red-Blue Flash", primary: blink1.State{Blue: 255}, secondary: blink1.State{Red: 255}, primaryFlash: time.Duration(500) * time.Millisecond, alternate: true}
	Blue         = CalendarState{Name: "Blue", primary: blink1.State{Blue: 255}, secondary: blink1.State{Blue: 255}}
	MagentaFlash = CalendarState{Name: "MagentaFlash", primary: blink1.State{Red: 255, Blue: 255}, secondary: blink1.OffState, primaryFlash: time.Duration(125) * time.Millisecond, alternate: true}
)

// Combines the two states into one state that shows both events
func CombineStates(in1 CalendarState, in2 CalendarState) CalendarState {
	combined := CalendarState{Name: in1.Name + "/" + in2.Name,
		primary:        in1.primary,
		secondary:      in2.primary,
		primaryFlash:   in1.primaryFlash,
		secondaryFlash: in2.primaryFlash,
		alternate:      false}
	return combined
}

// Swaps the sides for a state, for use in flashing
func SwapState(in CalendarState) CalendarState {
	swapped := CalendarState{Name: in.Name + " swapped",
		primary:        in.secondary,
		secondary:      in.primary,
		primaryFlash:   in.secondaryFlash,
		secondaryFlash: in.primaryFlash,
		alternate:      false}
	return swapped
}

// blinkerState encapsulates the current device state of the blink(1).
type BlinkerState struct {
	device      *blink1.Device
	newState    chan CalendarState
	failures    int
	maxFailures int
}

func NewBlinkerState(maxFailures int) *BlinkerState {
	blinker := &BlinkerState{
		newState:    make(chan CalendarState, 1),
		maxFailures: maxFailures,
	}
	blinker.reinitialize()
	return blinker
}

func (blinker *BlinkerState) reinitialize() error {
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

func (blinker *BlinkerState) setState(state blink1.State) error {
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

func (blinker *BlinkerState) patternRunner() {
	currentState := Black
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
			nextTick := state1.Duration
			if state1.Duration == 0 {
				nextTick = state2.Duration
			}
			fmt.Fprintf(debugOut, "Next tick: %s\n", nextTick)
			ticker = time.After(nextTick)
		}
	}
}

// Signal handler - SIGINT or SIGKILL should turn off the blinker before we exit.
// SIGQUIT should turn on debug mode.

func signalHandler(blinker *BlinkerState) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill, syscall.SIGQUIT)
	for {
		s := <-interrupt
		if s == syscall.SIGQUIT {
			fmt.Println("Turning on debug mode.")
			debugOut = os.Stdout
			continue
		}
		if blinker.failures == 0 {
			blinker.newState <- Black
			blinker.device.SetState(blink1.OffState)
		}
		log.Fatalf("Quitting due to signal %v", s)
	}
}
