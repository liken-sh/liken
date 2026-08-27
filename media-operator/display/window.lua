-- The window watchdog. mpv tolerates a video output it could not
-- open: it logs "Failed initializing any suitable GPU context!" and
-- keeps running, which for the idle client is a process that draws
-- nothing while the screen shows the compositor's background. That
-- happens whenever the compositor restarts under a running idle pod.
-- mpv has no option that exits on a lost window, and the project
-- patches no upstream dependency, so this module watches the property
-- and exits the player itself. The kubelet restarts the container with
-- backoff until the compositor answers again.
--
-- The watchdog is armed by IDLE_WINDOW_GRACE_SECONDS, which the
-- operator sets on the idle container alone. A playback pod sets it
-- nowhere: a Play on an audio-only unit expects no window, and an exit
-- there would end a run that is playing sound correctly.
local window = {}

-- The exit code this module quits with. It is above the codes
-- mpv exits with itself, so a person reading the container's last
-- state tells this exit from a failure inside mpv.
local EXIT_CODE = 7

local grace = 0
local timer = nil

local function stop()
  if timer then
    timer:kill()
    timer = nil
  end
end

-- The grace running out with no window. One line says what
-- happened and that the exit is deliberate, because a non-zero exit in
-- a log reads as a crash otherwise.
local function expire()
  timer = nil
  mp.msg.error(string.format(
    "no window after %d seconds; exiting %d so the kubelet restarts this container",
    grace, EXIT_CODE))
  mp.commandv("quit", tostring(EXIT_CODE))
end

-- A window that went away, or one that never arrived. The timer
-- already running is left alone, so a video output that fails again
-- while the grace runs does not extend it.
local function missing()
  if timer then
    return
  end
  timer = mp.add_timeout(grace, expire)
end

-- arm reads the grace in seconds. Anything but a positive number,
-- an unset variable included, leaves the watchdog off. mpv pushes the
-- property once when the script observes it, so a client that starts
-- with no window starts the grace at load.
function window.arm(seconds)
  local value = tonumber(seconds)
  if not value or value <= 0 then
    return
  end
  grace = value
  mp.observe_property("vo-configured", "bool", function(_, configured)
    if configured == true then
      stop()
    else
      missing()
    end
  end)
end

return window
