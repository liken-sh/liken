-- The input router and the summon state of the OSD. It holds which stop has
-- focus and whether a chooser captures, and it sends each press to the right
-- module. It draws no pixels.
local scrubber = require("scrubber")
local strip = require("strip")
local images = require("images")
local presentation = require("presentation")

local focus = {}

-- The focus stops, top to bottom. The scrubber owns two of them, fine and
-- chapter, on one bar. up and down walk the stops present for the current
-- file and skip the rest, so the list is flat and dynamic.
local STOPS = { "fine", "chapter", "images", "strip" }

-- Hide the OSD after this many idle seconds of play. A pause does not start
-- the countdown.
local IDLE_HIDE = 4

local summoned = false
local paused = false
local first_pause = true
local hide_timer = nil
local redraw_cb = function() end
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
  focused = p[at]
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
    summoned = false
    redraw_cb()
  end)
end

function focus.summon()
  if not summoned then
    summoned = true
    reset_focus()
  end
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
  redraw_cb()
end

-- main observes pause and reports it here. A pause summons the OSD and holds
-- it, and a resume starts the idle countdown.
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
    arm_hide()
  end
  redraw_cb()
end

-- back has two meanings, and the open chooser decides which. An open
-- chooser takes back to close itself. Everywhere else back dismisses the
-- OSD. A chooser is the only state that captures input, so it is the only
-- place back means "close me".
function focus.nav(action)
  if action == "back" then
    if capturing then
      capturing.close()
      capturing = nil
      redraw_cb()
    else
      focus.dismiss()
    end
    return
  end

  -- The press that wakes a hidden OSD only wakes it. It lands focus on the
  -- first stop, the fine playhead, and does not move or seek. The next press
  -- starts navigating, so a viewer sees where the film is before a press
  -- changes it.
  local was_visible = focus.visible()
  focus.summon()
  if not was_visible then
    return
  end

  -- A chooser is a vertical list, so up and down move its selection and
  -- select applies. left and right do nothing while it captures, and back,
  -- handled above, closes it.
  if capturing then
    if action == "up" or action == "down" or action == "select" then
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
