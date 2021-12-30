# Blink(1) for Google Calendar (calblink)

## What is this?

Calblink is a small program to watch your Google Calendar and set a blink(1) USB
LED to change colors based on your next meeting. The colors it will use are:

*   Off: nothing on your calendar for the next 60 minutes
*   Green: 30 to 60 minutes
*   Yellow: 10 to 30 minutes
*   Red: 5 to 10 minutes
*   Flashing red: 0 to 5 minutes, flashing faster for the last 2 minutes
*   Flashing blue and red: First minute of the meeting
*   Blue: In meeting
*   Flashing magenta: Unable to connect to Calendar server.  This is to prevent
    the case where calblink silently fails and leaves you unaware that it has
    failed.

## What do I need use it?

To use calblink, you need the following:

1.  A blink(1) from [ThingM](http://blink1.thingm.com/) - calblink supports
    mk1, mk2, and mk3 blink(1).
1.  A place to connect the blink(1) where you can see it.
2.  The latest version of [Go](https://golang.org/).
3.  The calblink code, found in this directory.
4.  libusb-compat. The [go-blink1](https://github.com/kazrakcom/go-blink1) page has
    details.
5.  A directory to run this in.
6.  A few go packages, which we'll install later in the Setup section.
7.  A Google Calendar account.
8.  A Google Calendar OAuth 2 client ID. (We'll discuss getting one in the Setup
    section as well.)

## How do I set this up?

1.  Install Go, and plug your blink(1) in somewhere that you can see it.
2.  Bring up a command-line window, and create the directory you want to run
    this in.
3.  Put calblink.go into the directory you just created.
4.  Install libusb-compat, if needed. If you needed to, and used Homebrew as instructed
    above, set INCLUDE\_PATH and LIBRARY\_PATH to point to the 'include' and
    'lib' directories under the Homebrew directory. 
5.  Create your module file:
    ```
    go mod init calblink
    go mod tidy
    ```
    
7.  Get an OAuth 2 ID as described in step 1 of the [Google Calendar
    Quickstart](https://developers.google.com/google-apps/calendar/quickstart/go).
    Put the client\_secret.json file in your GOPATH directory.

8.  Run the calblink program: go run calblink.go

9.  It will request that you go to a URL and give it the token that you get
    back. You should access this URL from the account you want to read the
    calendar of.

10. That's it! It should just run now, and set your blink(1) to change color
    appropriately. To quit out of it, hit Ctrl-C in the window you ran it in.
    (It will turn the blink(1) off automatically.) It will output a . into the
    terminal window every time it checks the server and sets the LED.

11. Optionally, set up a config file, as below.

## What are the configuration options?

First off, run it with the --help option to see what the command-line options
are. Useful, perhaps, but maybe not what you want to use every time you run it.

calblink will look for a file named (by default) conf.json for its configuration
options. conf.json includes several useful options you can set:

*   excludes - a list of event titles which it will ignore. If you like blocking
    out time with "Make Time" or similar, you can add these names to the
    'excludes' array.
*   excludePrefix - a list of event title prefixes which it will ignore.  This is useful
    for blocks that start consistently but may not end consistently, such as "On call,
    secondary is PERSON".
*   startTime - an HH:MM time (24-hour clock) which calblink won't turn on
    before. Because you might not want it turning on at 4am.
*   endTime - an HH:MM time (24-hour clock) which it won't turn on after.
*   skipDays - a list of days of the week that it should skip. A blink(1) in
    the offices doesn't need to run on Saturday/Sunday, after all, and if you
    WFH every Friday, why distract your coworkers?
*   pollInterval - how often (in seconds) it should check with Calendar for an
    update. Default is 30 seconds. Don't push this too frequent or you'll run
    out of API quota.
*   calendar - which calendar to watch (defaults to primary). This is the email
    address of the calendar - either the calendar's owner, or the ID in its
    details page for a secondary calendar. "primary" is a magic string that
    means "the main calendar of the account whose auth token I'm using".
*   responseState - which response states are marked as being valid for a
    meeting. Can be set to "all", in which case any item on your calendar will
    light up; "accepted", in which case only items marked as 'accepted' on
    calendar will light up; or "notRejected", in which case items that you have
    rejected will not light up. Default is "notRejected".
*   deviceFailureRetries - how many times to retry accessing the blink(1) before
    failing out and terminating the program. Default is 10.
*   showDots - whether to show a dot (or similar mark) after every poll interval
    to show that the program is running. Default is true. Symbols have the
    following meanings:
    *    . - working normally
    *    , - unable to talk to the calendar server. After 3 consecutive failures,
         the blink(1) will be set to flashing magenta to indicate that it is no
         longer current.
    *    < - sleeping because we've reached endTime for today.
    *    \> - sleeping because we haven't reached startTime yet today.
    *    ~ - sleeping because it's a skip day
    *    X - device failure.
*   multiEvent - if true, calblink will check the next two events, and if they are
    both in the time frame to show, it will show both.

An example file:

```json
    {
        "excludes": ["Commute"],
        "skipDays": ["Saturday", "Sunday"],
        "startTime": "08:45",
        "endTime": "18:00",
        "pollInterval": 60,
        "calendar": "username@example.com",
        "responseState": "accepted"
        "multiEvent": "true"
    }
```

(Yes, the curly braces are required.  Sorry.  It's a JSON dictionary.)

## Known Issues

*   If there are more than 20 events that are skipped (all-day events, excluded
    events, and events with the wrong responseState) before the event that
    should be shown, the event will not be processed.

## Troubleshooting

*   If the blink(1) is flashing magenta, this means it was unable to connect to
    or authenticate to the Google Calendar server.  If your network is okay, your
    auth token may have expired.  Remove ~/.credentials/calendar-blink1.json and
    reconnect the app to your account.
*   Another reason it may flash magenta is an issue with Go 1.8 and Xcode 8.3
    or later. Upgrade to Go 1.8.1 to fix this issue.
*   If attempting to install the blink1 go library or run calblink.go on OSX
    gives an error about "'usb.h' file not found", make sure that C_INCLUDE_PATH
    and LIBRARY_PATH are set appropriately.
*   Sending a SIGQUIT will turn on debug mode while the app is running.  By
    default on Unix-based systems, this is sent by hitting Ctrl-\\ (backslash).
    There is currently no way to turn debug mode off once it is set.

## Legal

*   Calblink is not an official Google product.
*   Calblink is licensed under the Apache 2 license; see the LICENSE file for details.
*   Calblink contains code from the [Google Calendar API
    Quickstart](https://developers.google.com/google-apps/calendar/quickstart/go)
    which is licensed under the Apache 2 license.
*   All trademarks are the property of their respective holders.
