-- The fine scrubber. It owns its geometry, its three time labels, the
-- playhead, and the accelerating seek.
local theme = require("theme")

local seekbar = {}

local redraw_cb = function() end
function seekbar.set_redraw(fn)
  redraw_cb = fn
end

-- geometry
local LEFT = theme.margin.x
local RIGHT = theme.canvas.w - theme.margin.x
local BAR_W = RIGHT - LEFT
local BAR_Y = 900
local BAR_H = 8
-- The current time rides above the bar; the time left and the total length
-- sit below it.
local TOP_Y = 880
local BELOW_Y = 956
local KNOB_R = 11

-- seek and acceleration
-- The seek is time based, not press based, so the speed does not depend on
-- the keyboard or the controller repeat rate. The first press of a gesture
-- nudges TAP_STEP seconds. A hold ramps the speed from MIN_RATE to MAX_RATE
-- seconds of film per second of hold, reaching MAX_RATE after RAMP seconds.
local TAP_STEP = 5
local MIN_RATE = 30
local MAX_RATE = 300
local RAMP = 4
-- A gap longer than this ends the gesture, so the next press is a tap and
-- the ramp starts again.
local GAP = 0.25
-- Playback follows the cursor on this interval while scrubbing, so the
-- frames track the playhead instead of jumping only at the end.
local COMMIT_INTERVAL = 0.1
-- After this idle the cursor drops and the display tracks time-pos again.
local RESET_IDLE = 0.4

local cursor = nil
local last_dir = 0
local last_event = 0
local hold_start = 0
local commit_timer = nil
local reset_timer = nil

local function kill(t)
  if t then
    t:kill()
  end
end

-- A keyframe seek while scrubbing is fast, so the frames keep up with the
-- cursor. The settle at the end of the gesture lands on the exact frame.
local function commit(mode)
  if cursor then
    mp.commandv("seek", cursor, "absolute+" .. mode)
  end
end

function seekbar.seek(dir)
  local dur = mp.get_property_number("duration")
  if not dur or dur <= 0 then
    return
  end

  local now = mp.get_time()
  local dt = now - last_event
  if cursor == nil then
    cursor = mp.get_property_number("time-pos") or 0
  end

  if dir ~= last_dir or dt > GAP then
    hold_start = now
    cursor = cursor + dir * TAP_STEP
  else
    local held = now - hold_start
    local rate = MIN_RATE + (MAX_RATE - MIN_RATE) * math.min(1, held / RAMP)
    cursor = cursor + dir * rate * dt
  end
  last_dir = dir
  last_event = now
  cursor = math.max(0, math.min(dur, cursor))

  if not commit_timer then
    commit_timer = mp.add_timeout(COMMIT_INTERVAL, function()
      commit_timer = nil
      commit("keyframes")
    end)
  end

  kill(reset_timer)
  reset_timer = mp.add_timeout(RESET_IDLE, function()
    reset_timer = nil
    commit("exact")
    last_dir = 0
    cursor = nil
    redraw_cb()
  end)

  redraw_cb()
end

local function fmt(t)
  t = math.floor(t + 0.5)
  local h = math.floor(t / 3600)
  local m = math.floor((t % 3600) / 60)
  local s = t % 60
  if h > 0 then
    return string.format("%d:%02d:%02d", h, m, s)
  end
  return string.format("%d:%02d", m, s)
end

-- The remaining time in whole minutes, phrased for a person. Under a minute
-- reads as such, and one minute drops the plural.
local function remaining_phrase(secs)
  local minutes = math.floor(secs / 60 + 0.5)
  if minutes <= 0 then
    return "less than a minute remaining"
  end
  if minutes == 1 then
    return "1 minute remaining"
  end
  return string.format("%d minutes remaining", minutes)
end

function seekbar.draw()
  local dur = mp.get_property_number("duration")
  if not dur or dur <= 0 then
    return nil
  end
  -- Draw from the cursor while a seek is in flight, so the playhead leads the
  -- debounced seek. Fall back to time-pos after the reset. Without this the
  -- playhead stutters behind the seeks.
  local pos = cursor or mp.get_property_number("time-pos") or 0
  pos = math.max(0, math.min(dur, pos))

  local frac = pos / dur
  local fillw = BAR_W * frac
  local knobx = LEFT + fillw

  local parts = {}
  parts[#parts + 1] = theme.rounded_rect(
    LEFT, BAR_Y - BAR_H / 2, BAR_W, BAR_H, BAR_H / 2, theme.color.track, theme.alpha.track
  )
  parts[#parts + 1] = theme.rounded_rect(
    LEFT, BAR_Y - BAR_H / 2, math.max(fillw, BAR_H), BAR_H, BAR_H / 2, theme.color.fill, theme.alpha.opaque
  )
  parts[#parts + 1] = theme.hexagon(
    knobx - KNOB_R, BAR_Y - KNOB_R, KNOB_R, theme.color.playhead, theme.alpha.opaque
  )

  -- The current time rides above the playhead and moves with it, so the eye
  -- reads the position where it is already looking. This is also where the
  -- trickplay thumbnail sits in a later slice. The x is clamped to the bar,
  -- so the label stays on screen at either end.
  local head_x = math.max(LEFT, math.min(RIGHT, knobx))
  parts[#parts + 1] = theme.text(head_x, TOP_Y, fmt(pos), theme.type.label, theme.color.text, 2)

  -- Below the bar, right aligned: the plain-language time left and the exact
  -- total length on one line, clear of the moving head time above.
  parts[#parts + 1] = theme.text(
    RIGHT, BELOW_Y, remaining_phrase(dur - pos) .. "  \194\183  " .. fmt(dur),
    theme.type.small, theme.color.text, 3
  )

  return table.concat(parts, "\n")
end

return seekbar
