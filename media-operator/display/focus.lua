-- The input router and the summon state of the OSD. It holds which stop has
-- focus and whether a chooser captures, and it sends each press to the right
-- module. It draws no pixels.
local theme = require("theme")
local scrubber = require("scrubber")
local strip = require("strip")
local images = require("images")
local presentation = require("presentation")

local focus = {}

-- The OSD fades in over FADE_IN_MS and out over FADE_OUT_MS. The out is longer
-- than the in, so the OSD fades out more slowly than it fades in. The numbers
-- live in theme, because the volume indicator fades on a clock of its own and
-- the two clocks must run at the same rates. Tune them there.
local FADE_IN_MS = theme.fade_in_ms
local FADE_OUT_MS = theme.fade_out_ms
-- The fade steps on this period, about sixty times a second, and requests a
-- redraw on each step.
local FADE_TICK = theme.fade_tick

-- The focus stops, top to bottom. The scrubber owns two of them, fine and
-- chapter, on one bar. up and down walk the stops present for the current
-- file and skip the rest, so the list is flat and dynamic.
local STOPS = { "fine", "chapter", "images", "strip" }

-- Hide the OSD after this many idle seconds of play. A pause does not start
-- the countdown. The number lives in theme, because the volume indicator
-- waits out the same window on a timer of its own.
local IDLE_HIDE = theme.idle_hide

-- The script-message the display and the command sidecar agree on for the exit
-- press. The sidecar answers it: it publishes the ending to the bus and then
-- quits mpv. The display does not quit mpv itself, because the operator must
-- read the ending while the film is still on the display. It then draws the
-- idle screen over the film that is shutting down, with no black gap between
-- the two.
local EXIT = "liken-exit"

local summoned = false
local paused = false
local first_pause = true
local hide_timer = nil
-- The current fade factor, the target it steps toward, and the timer that steps
-- it. theme reads the factor to scale every alpha.
local fade = 0
local fade_target = 0
local fade_timer = nil
local redraw_cb = function() end
theme.set_fade(fade)
-- The stop that has focus, and the control whose chooser captures input.
local focused = nil
local capturing = nil

function focus.set_redraw(fn)
  redraw_cb = fn
end

local function stop_available(stop)
  if stop == "fine" then
    return presentation.type() ~= "image" and scrubber.fine_available()
  elseif stop == "chapter" then
    return presentation.type() ~= "image" and scrubber.chapter_available()
  elseif stop == "images" then
    return images.available()
  elseif stop == "strip" then
    return strip.available()
  end
  return false
end

-- A stop takes focus only when it has something to show, so up and down walk
-- the present stops and skip the rest.
local function present()
  local out = {}
  for _, s in ipairs(STOPS) do
    if stop_available(s) then
      out[#out + 1] = s
    end
  end
  return out
end

function focus.focused_stop()
  return focused
end

function focus.capturing()
  return capturing
end

function focus.visible()
  return summoned
end

-- The fade factor, so main draws the layout while it is above 0 and clears the
-- overlay once it reaches 0.
function focus.fade()
  return fade
end

-- Step the factor toward its target on each tick, at the in-rate on the way up
-- and the out-rate on the way down, then redraw. Stop the timer at the target.
local function fade_step()
  local rate_ms = fade_target > fade and FADE_IN_MS or FADE_OUT_MS
  local step = FADE_TICK * 1000 / rate_ms
  if fade_target > fade then
    fade = math.min(fade_target, fade + step)
  else
    fade = math.max(fade_target, fade - step)
  end
  theme.set_fade(fade)
  redraw_cb()
  if fade == fade_target and fade_timer then
    fade_timer:kill()
    fade_timer = nil
  end
end

-- Set the target and run the timer until the factor reaches it. One timer serves
-- both directions, so a dismiss during a fade-in reverses the same timer in place.
local function start_fade(target)
  fade_target = target
  if fade == fade_target then
    return
  end
  if not fade_timer then
    fade_timer = mp.add_periodic_timer(FADE_TICK, fade_step)
  end
end

local function reset_focus()
  local p = present()
  focused = p[1]
end

local function move(dir)
  local p = present()
  local at = 1
  for i, s in ipairs(p) do
    if s == focused then
      at = i
    end
  end
  at = math.max(1, math.min(#p, at + dir))
  local next_stop = p[at]
  -- Leaving the fine stop drops a scan in flight, so a preview does not outlive
  -- the move to another control.
  if focused == "fine" and next_stop ~= "fine" and scrubber.scanning() then
    scrubber.cancel()
  end
  focused = next_stop
end

-- Route the horizontal and select presses to the focused stop. The fine and
-- chapter stops seek and step on the scrubber. The strip moves its control
-- focus, and select returns a chooser to capture.
local function route(action)
  if focused == "fine" then
    if action == "left" then
      scrubber.seek(-1)
    elseif action == "right" then
      scrubber.seek(1)
    elseif action == "select" then
      scrubber.commit()
    end
    return nil
  elseif focused == "chapter" then
    if action == "left" then
      scrubber.chapter_step(-1)
    elseif action == "right" then
      scrubber.chapter_step(1)
    end
    return nil
  elseif focused == "images" then
    return images.press(action)
  elseif focused == "strip" then
    return strip.press(action)
  end
  return nil
end

local function cancel_hide()
  if hide_timer then
    hide_timer:kill()
    hide_timer = nil
  end
end

local function arm_hide()
  cancel_hide()
  if paused then
    return
  end
  hide_timer = mp.add_timeout(IDLE_HIDE, function()
    hide_timer = nil
    focus.dismiss()
  end)
end

-- Toggle mpv's pause. The pause observer calls on_pause, which summons the OSD,
-- so a pause from select needs no separate summon. The no-osd prefix suppresses
-- mpv's own pause indicator, so the liken OSD is the only thing that draws.
local function toggle_pause()
  mp.commandv("no-osd", "cycle", "pause")
end

function focus.summon()
  if not summoned then
    summoned = true
    reset_focus()
  end
  start_fade(1)
  arm_hide()
  redraw_cb()
end

function focus.dismiss()
  summoned = false
  if capturing then
    capturing.close()
    capturing = nil
  end
  cancel_hide()
  start_fade(0)
  redraw_cb()
end

-- main observes pause and reports it here. A pause summons the OSD and holds
-- it, and a resume dismisses it at once.
function focus.on_pause(p)
  paused = p
  -- mpv fires this callback once at startup with the current pause state. The
  -- first call only records it, so a film that loads paused starts with the
  -- OSD hidden. A later pause, during playback, summons the OSD.
  if first_pause then
    first_pause = false
    return
  end
  if p then
    focus.summon()
    cancel_hide()
  elseif summoned then
    focus.dismiss()
  end
  redraw_cb()
end

-- select carries the main action and play/pause on one button. It acts on an
-- open chooser, the focused strip control, or a fine scan in flight, and
-- otherwise toggles play/pause. So the button confirms a choice when the OSD
-- has one to make, and plays or pauses the film when it does not.
function focus.select()
  if capturing then
    capturing.handle("select")
    if not capturing.is_open() then
      capturing = nil
    end
    redraw_cb()
    return
  end
  if focus.visible() then
    if focused == "strip" then
      capturing = route("select")
      redraw_cb()
      return
    end
    if focused == "fine" and scrubber.scanning() then
      scrubber.commit()
      redraw_cb()
      return
    end
  end
  toggle_pause()
end

-- back has four meanings, one per state, tried in order. An open chooser closes.
-- Else a fine scan cancels its preview. Else the visible OSD dismisses. Else, at
-- the bare video, back asks the command sidecar to end the run, and the sidecar
-- quits mpv with code 0, so the pod ends as the Completed a finished film gives,
-- not an Error.
function focus.nav(action)
  if action == "back" then
    if capturing then
      capturing.close()
      capturing = nil
      redraw_cb()
    elseif focused == "fine" and scrubber.scanning() then
      -- A scan is in flight, so back cancels the preview and leaves the video
      -- where it plays.
      scrubber.cancel()
      redraw_cb()
    elseif focus.visible() then
      focus.dismiss()
    else
      mp.command_native({ "script-message", EXIT })
    end
    return
  end

  if action == "select" then
    focus.select()
    return
  end

  -- The press that wakes a hidden OSD only wakes it. It lands focus on the
  -- first stop present, and does not move or seek. The next press starts
  -- navigating, so a viewer sees where the film is before a press changes it.
  local was_visible = focus.visible()
  focus.summon()
  if not was_visible then
    return
  end

  -- A captured widget receives up, down, left, right, and select. A chooser is
  -- a vertical list, so it moves on up and down, applies on select, and ignores
  -- left and right. The A/V sync adjuster uses left and right to nudge a delay.
  -- back, handled above, closes the widget.
  if capturing then
    if
      action == "up"
      or action == "down"
      or action == "select"
      or action == "left"
      or action == "right"
    then
      capturing.handle(action)
      if not capturing.is_open() then
        capturing = nil
      end
    end
    redraw_cb()
    return
  end

  if action == "up" then
    move(-1)
  elseif action == "down" then
    move(1)
  else
    capturing = route(action)
  end
  redraw_cb()
end

return focus
