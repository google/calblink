# Running calblink as a service

## What does this do?

Calblink now supports a mode where it runs as a service.  This means that it is managed
by your operating system instead of needing to manually run it.  This service can be
turned on and survive reboots.

## What operating systems does this mode support?

It has only been tested on macOS.  Theoretically it should work on Linux and other
Unix-style operating systems, and might possibly work on Windows.  Try it out and if
you have issues, let me know.

## What potential problems are there for this mode?

calblink currently doesn't cope well with not having a blink(1) installed when it is run.
It will exit after enough failures to control the blink(1), if it doesn't segfault first.
This mode works best for cases where a machine has a blink(1) set up at all times.
Alternately, if you have a way of controlling launch daemons based on USB events
(EventScripts or similar on macOS) you can use that to only run calblink when there
is a blink(1) plugged in.

If you don't disable the launch daemon when there isn't a blink(1) plugged in, calblink
will crash and be automatically restarted every ten seconds or so.

## How do I set this up?

These instructions assume macOS.

1.  Install calblink like you normally would, then make sure your configuration
    is set up the way you want.
2.  Run calblink as follows:

        ./calblink -runAsService -service install
        
    This will install a launch agent in ~/Library/LaunchAgents.
3.  You can then control it with launchctl like any other launch agent, or run
    calblink to control the agent:

        ./calblink -runAsService -service start
        
    Available commands include `start`, `stop`, `restart`, `install`, and `uninstall`.
4.  Log messages will go into your home directory, in `calblink.out.log` and
    `calblink.err.log`.  Unless debug is turned on, there should be minimal logging.
    One log line is created at startup and shutdown, and fatal errors will be logged
    to the error log.
