-- The shade, one full-canvas black rectangle the idle screen draws over
-- everything else. The sidecar owns the quiet timer and sends
-- player-sleep and player-wake; this module only eases its cover
-- between clear and opaque. Going dark is slow and coming back is
-- fast, because a screen that fades on its own has no one watching it,
-- and a person who pressed a button is waiting.
local theme = require("theme")

local shade = {}

-- The ease timer runs at thirty frames a second and exists only while
-- the shade moves, so a screen that has settled, dark or clear, keeps
-- the one-second clock tick as its only timer.
local TICK = 1 / 30

-- Four seconds down, and under half a second back up.
local SLEEP_MS = 4000
local WAKE_MS = 400

-- The current cover, 0 clear and 1 opaque, the ends of the ease in
-- flight, and how far that ease has run.
local level = 0
local ease_from = 0
local ease_to = 0
local ease_ms = SLEEP_MS
local ease_elapsed = 0
local easing = false

local timer = nil
local redraw_cb = function() end

function shade.set_redraw(fn)
  redraw_cb = fn
end

-- Smoothstep, the curve that starts and ends at zero slope, the same
-- ease energy.lua runs its ramps on.
local function ease(t)
  return t * t * (3 - 2 * t)
end

local function stop_timer()
  if timer then
    timer:kill()
    timer = nil
  end
end

-- Advance the ease by one frame, ask for a redraw, and stop the timer
-- once the ease lands.
local function step()
  ease_elapsed = ease_elapsed + TICK * 1000
  local t = math.min(1, ease_elapsed / ease_ms)
  level = ease_from + (ease_to - ease_from) * ease(t)
  if t >= 1 then
    level = ease_to
    easing = false
  end
  redraw_cb()
  if not easing then
    stop_timer()
  end
end

local function start_timer()
  if not timer then
    timer = mp.add_periodic_timer(TICK, step)
  end
end

-- Ease the cover to a target over a duration. An ease already in
-- flight toward that target runs on, and a reversal turns around from
-- the current level, so the cover never jumps.
local function run(target, ms)
  if not easing and level == target then
    return
  end
  if easing and ease_to == target then
    return
  end
  ease_from = level
  ease_to = target
  ease_ms = ms
  ease_elapsed = 0
  easing = true
  start_timer()
end

function shade.on_sleep()
  run(1, SLEEP_MS)
end

function shade.on_wake()
  run(0, WAKE_MS)
end

-- The cover as an ASS alpha byte. ASS alpha runs 00 opaque to FF
-- transparent, the reverse of the level.
local function cover_alpha()
  return string.format("&H%02X&", math.floor((1 - level) * 255 + 0.5))
end

-- The shade's ASS, or nil while the screen is fully clear, so an awake
-- idle screen adds no shape to its overlay.
function shade.draw()
  if level <= 0 then
    return nil
  end
  return theme.rect(0, 0, theme.canvas.w, theme.canvas.h, theme.color.shadow, cover_alpha())
end

return shade
