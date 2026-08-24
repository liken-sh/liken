-- The energy, the one scalar that drives every animation on the idle screen. It
-- runs from 0, the mark at rest, to 1, the mark at full swing. The brand
-- repository's motion.md sets the rule this module implements: the mark moves
-- only while the system works on something a person waits for, and every change
-- of energy eases rather than steps.
--
-- The activity in the Player's retained status decides the target. Starting
-- ramps the energy up, because the seconds a playback pod pulls and starts are
-- the seconds a person waits. Playing stops the animation outright, because the
-- film's surface covers the idle surface and a frame drawn behind it is never
-- seen. Anything else eases the energy back to 0.
--
-- The module holds the animation phase as well as the level, so the two move on
-- one timer. The phase advances faster at a high energy than at a low one, which
-- is how the energy scales the speed and the swing together. The phase is never
-- reset, so a change of energy changes the rate of the motion and never its
-- position, and the mark never jumps.
local energy = {}

-- The frame timer runs at thirty frames a second. It exists only while the
-- energy stands above 0 or still eases, so a settled idle screen keeps the
-- one-second clock tick as its only timer.
local TICK = 1 / 30

-- The ramp up is shorter than the ramp down, so the mark reaches full swing
-- quickly when a Play starts, and returns to rest slowly when the film ends.
local RAMP_UP_MS = 1200
local RAMP_DOWN_MS = 2500

-- The phase advances at this fraction of its full rate at energy 0, and at the
-- full rate at energy 1. The floor is above 0 so a ramp-down slows the motion
-- without freezing it before the swing itself reaches 0.
local SPEED_FLOOR = 0.3

-- The current level, the ends of the ramp in flight, and how far that ramp has
-- run. ramping is false once the level reaches the target, and the timer stops
-- when nothing ramps and the level is 0.
local level = 0
local ramp_from = 0
local ramp_to = 0
local ramp_ms = RAMP_UP_MS
local ramp_elapsed = 0
local ramping = false

local phase = 0
local timer = nil
local last_activity = nil
local redraw_cb = function() end

function energy.set_redraw(fn)
  redraw_cb = fn
end

function energy.level()
  return level
end

function energy.phase()
  return phase
end

-- The activity from the last status, so a reveal reads the state the screen came
-- back into.
function energy.activity()
  return last_activity
end

-- Smoothstep, the curve that starts and ends at zero slope. It is what makes the
-- ramp read as an ease and not as a straight climb.
local function ease(t)
  return t * t * (3 - 2 * t)
end

local function stop_timer()
  if timer then
    timer:kill()
    timer = nil
  end
end

-- Advance the ramp and the phase by one frame, then ask for a redraw. The phase
-- advance reads the level after the ramp step, so a falling energy slows the
-- motion on the same frame it shrinks it.
local function step()
  if ramping then
    ramp_elapsed = ramp_elapsed + TICK * 1000
    local t = math.min(1, ramp_elapsed / ramp_ms)
    level = ramp_from + (ramp_to - ramp_from) * ease(t)
    if t >= 1 then
      level = ramp_to
      ramping = false
    end
  end
  phase = phase + TICK * (SPEED_FLOOR + (1 - SPEED_FLOOR) * level)
  redraw_cb()
  if not ramping and level <= 0 then
    stop_timer()
  end
end

local function start_timer()
  if not timer then
    timer = mp.add_periodic_timer(TICK, step)
  end
end

-- Ease the level to a target over a duration. A ramp that starts while another
-- ramp runs takes the current level as its start, so a reversal turns the motion
-- around from where it stands.
local function ramp(target, ms)
  if not ramping and level == target then
    return
  end
  -- A repeated status carries the same activity, and the operator publishes one
  -- on every pass over the Player. A ramp already in flight toward this target
  -- runs on, because restarting it from the level it reached would stretch the
  -- ramp for as long as the statuses keep arriving.
  if ramping and ramp_to == target and ramp_ms == ms then
    return
  end
  ramp_from = level
  ramp_to = target
  ramp_ms = ms
  ramp_elapsed = 0
  ramping = true
  start_timer()
end

-- on_status reads the activity of the Player's retained status. Playing stops
-- the timer and drops the level to 0 with no ease, which is the one place the
-- energy steps: nothing behind a running film is on screen to see it move. An
-- idle pod that starts in the middle of a film reads its first retained status
-- as Playing and lands here, so it starts still instead of animating.
function energy.on_status(activity)
  last_activity = activity
  if activity == "Playing" then
    stop_timer()
    level = 0
    ramping = false
    return
  end
  if activity == "Starting" then
    ramp(1, RAMP_UP_MS)
    return
  end
  ramp(0, RAMP_DOWN_MS)
end

-- on_revealed runs on the frame the sidecar recreated the idle surface and
-- showed it again, after a film ended. The mark arrives at full swing and eases
-- to rest, so the screen returns in motion rather than appearing frozen. A
-- reveal that lands while a Play starts or runs changes nothing, because that
-- activity owns the energy.
function energy.on_revealed()
  if last_activity == "Starting" or last_activity == "Playing" then
    return
  end
  level = 1
  ramp_from = 1
  ramp_to = 0
  ramp_ms = RAMP_DOWN_MS
  ramp_elapsed = 0
  ramping = true
  start_timer()
end

return energy
