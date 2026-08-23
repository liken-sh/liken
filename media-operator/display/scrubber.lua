-- The scrubber. It merges the fine seek and the chapter step into one
-- segmented bar. One segment per chapter marks the coarse axis, and the
-- playhead and its time mark the fine axis. It owns both axes and draws once
-- per frame, told which axis has focus.
local theme = require("theme")

local scrubber = {}

local redraw_cb = function() end
function scrubber.set_redraw(fn)
  redraw_cb = fn
end

-- geometry
local LEFT = theme.margin.x
local RIGHT = theme.canvas.w - theme.margin.x
local BAR_W = RIGHT - LEFT
local BAR_Y = 904
local BAR_H = 14
-- The gap between two segments, so the divisions read as separate chapters.
local SEG_GAP = 4
local SEG_R = 3
-- The time sits this far above the bar center, and the line below sits this far
-- under it. The gaps are named, so the vertical spacing is one place to tune.
local TIME_ABOVE = 18
local BELOW_GAP = 18
-- The current time rides above the bar; the chapter title and the time left
-- sit below it.
local TOP_Y = BAR_Y - TIME_ABOVE
local BELOW_Y = BAR_Y + BELOW_GAP
-- The playhead stands proud of the bar, so its points read against the video
-- above and below the green fill it rides on.
local KNOB_R = 15
-- The dark outline separates the green playhead from the green fill under it,
-- so the head reads against the fill.
local KNOB_BORDER = 2

-- scan and acceleration
-- The scan is time based, not press based, so the speed does not depend on the
-- keyboard or the controller repeat rate. The first press of a gesture moves
-- the cursor TAP_STEP seconds. A hold ramps the speed from MIN_RATE to MAX_RATE
-- seconds of film per second of hold, reaching MAX_RATE after RAMP seconds.
local TAP_STEP = 5
local MIN_RATE = 30
local MAX_RATE = 300
local RAMP = 4
-- A gap longer than this ends the gesture, so the next press is a tap and
-- the ramp starts again.
local GAP = 0.25

local cursor = nil
local last_dir = 0
local last_event = 0
local hold_start = 0

-- A fine scan previews a target without moving the video. left and right move
-- the cursor, and the thumbnail shows the frame there, while the video plays on
-- at its own position. So the preview is the only thing that moves, and the
-- scan costs one crop per tile and no seek. A select commits the seek, and a
-- cancel drops the cursor. Without this the video would chase the cursor, and
-- the thumbnail would show the frame already on screen.
function scrubber.seek(dir)
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

  redraw_cb()
end

-- scanning reports whether a scan is in flight, so the router sends select and
-- back to the scan, and the display shows the thumbnail only then.
function scrubber.scanning()
  return cursor ~= nil
end

-- commit lands the seek on the previewed frame, so a select ends the scan at
-- the target. The exact seek lands the frame the thumbnail showed.
function scrubber.commit()
  if cursor then
    mp.commandv("seek", cursor, "absolute+exact")
    cursor = nil
    last_dir = 0
    redraw_cb()
  end
end

-- cancel drops the preview with no seek, so a back leaves the video where it
-- plays. Leaving the fine stop cancels the scan the same way.
function scrubber.cancel()
  if cursor then
    cursor = nil
    last_dir = 0
    redraw_cb()
  end
end

-- Stepping a chapter moves the playback position, so the fine playhead
-- follows. This is the coarse seek axis, one chapter a press, on the same
-- bar as the fine scrubber that moves in seconds.
function scrubber.chapter_step(dir)
  mp.commandv("add", "chapter", dir)
  redraw_cb()
end

local function chapter_list()
  return mp.get_property_native("chapter-list") or {}
end

function scrubber.fine_available()
  local dur = mp.get_property_number("duration")
  return dur ~= nil and dur > 0
end

function scrubber.chapter_available()
  return #chapter_list() > 0
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

-- The two focus axes brighten different groups. The position group is the
-- playhead, its time, and the progress fill. The chapter group is the
-- current chapter segment and the title. The axis argument brightens one
-- group and subdues the other, and nil subdues both.
local function brightness(axis)
  local sub = theme.alpha.subdued
  local on = theme.alpha.opaque
  if axis == "fine" then
    return on, sub
  elseif axis == "chapter" then
    return sub, on
  end
  return sub, sub
end

-- The segments for the bar. With chapters, one box per chapter with a gap on
-- its right. With no chapters, one continuous box across the full width.
local function segments(dur)
  local chs = chapter_list()
  if #chs == 0 then
    return { { x0 = LEFT, x1 = RIGHT, w = BAR_W } }
  end
  local out = {}
  for i, ch in ipairs(chs) do
    local start = ch.time or 0
    local finish = (chs[i + 1] and chs[i + 1].time) or dur
    local x0 = LEFT + BAR_W * (start / dur)
    local x1 = LEFT + BAR_W * (finish / dur)
    out[i] = { x0 = x0, x1 = x1, w = math.max(2, x1 - x0 - SEG_GAP) }
  end
  return out
end

-- Draw from the cursor while a seek is in flight, so the playhead leads the
-- debounced seek. Fall back to time-pos after the reset. Without this the
-- playhead stutters behind the seeks.
local function displayed_pos(dur)
  local pos = cursor or mp.get_property_number("time-pos") or 0
  return math.max(0, math.min(dur, pos))
end

local function pos_to_x(dur, pos)
  return LEFT + BAR_W * (pos / dur)
end

-- The displayed position in seconds, the in-flight cursor or time-pos, and nil
-- with no duration. The thumbnail reads it to pick the frame to show.
function scrubber.cursor_time()
  local dur = mp.get_property_number("duration")
  if not dur or dur <= 0 then
    return nil
  end
  return displayed_pos(dur)
end

-- The playhead x in canvas coordinates, the same value draw computes, and nil
-- with no duration. The thumbnail centers on it.
function scrubber.cursor_x()
  local dur = mp.get_property_number("duration")
  if not dur or dur <= 0 then
    return nil
  end
  return pos_to_x(dur, displayed_pos(dur))
end

function scrubber.draw(axis)
  local dur = mp.get_property_number("duration")
  if not dur or dur <= 0 then
    return nil
  end
  local pos_a, chap_a = brightness(axis)

  local pos = displayed_pos(dur)
  local knobx = pos_to_x(dur, pos)

  local chs = chapter_list()
  local cur = mp.get_property_number("chapter") or 0

  local segs = segments(dur)
  local top = BAR_Y - BAR_H / 2

  local parts = {}
  for i, seg in ipairs(segs) do
    -- The deep green track box marks the segment. The bright green fill
    -- covers it up to the playhead, so a segment before the head fills whole,
    -- the segment under the head fills part way, and a segment after stays the
    -- darker track green.
    parts[#parts + 1] = theme.rounded_rect(
      seg.x0, top, seg.w, BAR_H, SEG_R, theme.color.track, theme.alpha.track
    )
    local fill_right = math.max(seg.x0, math.min(seg.x0 + seg.w, knobx))
    local fillw = fill_right - seg.x0
    if fillw >= 1 then
      parts[#parts + 1] = theme.rounded_rect(
        seg.x0, top, fillw, BAR_H, SEG_R, theme.color.fill, pos_a
      )
    end
    -- The chapter axis marks the current chapter by filling its whole
    -- segment green, brighter than the progress fill under it.
    if axis == "chapter" and #chs > 0 and (i - 1) == cur then
      parts[#parts + 1] = theme.rounded_rect(
        seg.x0, top, seg.w, BAR_H, SEG_R, theme.color.fill, chap_a
      )
    end
  end

  -- During a scan the video plays on while the cursor previews elsewhere, so a
  -- thin tick marks where playback is. The playhead draws after it, so the two
  -- read apart when they overlap.
  if cursor then
    local live = math.max(0, math.min(dur, mp.get_property_number("time-pos") or 0))
    local livex = pos_to_x(dur, live)
    parts[#parts + 1] = theme.rect(livex - 1, top - 6, 2, BAR_H + 12, theme.color.text, theme.alpha.subdued)
  end

  parts[#parts + 1] = theme.hexagon(
    knobx - KNOB_R, BAR_Y - KNOB_R, KNOB_R, theme.color.playhead, pos_a,
    KNOB_BORDER, theme.color.shadow
  )

  -- The current time rides above the playhead and moves with it, so the eye
  -- reads the position where it is already looking. The x is clamped to the
  -- bar, so the label stays on screen at either end.
  local head_x = math.max(LEFT, math.min(RIGHT, knobx))
  parts[#parts + 1] = theme.text(head_x, TOP_Y, fmt(pos), theme.type.label, theme.color.text, 2, pos_a)

  -- Below the bar on the left, the chapter title and position. Below on the
  -- right, the plain-language time left and the exact total length.
  if #chs > 0 then
    local title = chs[cur + 1] and chs[cur + 1].title
    local label = string.format("%d of %d", cur + 1, #chs)
    if title and title ~= "" then
      label = title .. "   \194\183   " .. label
    end
    parts[#parts + 1] = theme.text(LEFT, BELOW_Y, label, theme.type.small, theme.color.text, 7, chap_a)
  end
  parts[#parts + 1] = theme.text(
    RIGHT, BELOW_Y, remaining_phrase(dur - pos) .. "  \194\183  " .. fmt(dur),
    theme.type.small, theme.color.text, 9, pos_a
  )

  return table.concat(parts, "\n")
end

return scrubber
