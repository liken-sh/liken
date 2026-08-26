-- The volume indicator: a speaker glyph, a short bar, and the number. It
-- comes and goes on a clock of its own, and it draws alone: a level change
-- brings up the indicator and nothing else on the display. The module only
-- reads mpv's volume and mute properties, because the bus holds the state
-- and the command sidecar owns every change to it.
local theme = require("theme")

local volume = {}

local redraw_cb = function() end
function volume.set_redraw(fn)
  redraw_cb = fn
end

-- The level runs 0 to 100, where 100 is unity, so the bar fills at 100 and
-- a level above it fills no further.
local FULL = 100

-- The row is the third line of the top-right column, under the clock and
-- the activity line, because the low center of the screen holds the
-- scrubber on a Play and the mark on the idle screen.
local function right()
  return theme.canvas.w - theme.margin.x
end
local ROW_Y = theme.margin.y + 2 * theme.line_pitch
-- The number reserves this much width at the right margin, so the bar and
-- the glyph hold their place as the number moves between one and three
-- digits.
local NUM_W = 84
-- The bar is short because the number beside it carries the reading, and
-- the bar shows the level at a glance.
local BAR_W = 220
local BAR_H = 12
local BAR_R = 3
-- The glyph's box, and the gap between the glyph and the bar.
local GLYPH_W = 26
local GLYPH_H = 30
local GLYPH_GAP = 16

-- The row appears alone over whatever frame is on screen, with no scrim
-- under it, and on a bright frame the glyph and the number would vanish. So
-- the row carries a dark surface of its own, at the scrim's edge alpha, and
-- this is the padding around the three parts.
local PAD_X = 24
local PAD_Y = 12
local SURFACE_R = 14

-- The bar and the glyph center on the middle of the number's line, so the
-- three parts read as one row.
local MID_Y = ROW_Y + theme.type.small / 2
local BAR_TOP = MID_Y - BAR_H / 2
local GLYPH_TOP = MID_Y - GLYPH_H / 2
local SURFACE_Y = ROW_Y - PAD_Y
-- The surface covers the three parts and the padding, and the number's
-- line is the tallest of the three.
local SURFACE_H = theme.type.small + 2 * PAD_Y

-- The three parts hang off the right margin, so the row measures itself
-- when it draws. The canvas width follows the screen, and a row measured at
-- load would hold the margin of another screen.
local function columns()
  local bar_x = right() - NUM_W - BAR_W
  local glyph_x = bar_x - GLYPH_GAP - GLYPH_W
  local surface_x = glyph_x - PAD_X
  return {
    bar_x = bar_x,
    glyph_x = glyph_x,
    surface_x = surface_x,
    surface_w = right() + PAD_X - surface_x,
  }
end

-- The speaker is one closed polygon, the driver box and the cone, drawn as
-- an ASS shape because the player image carries no icon font.
local SPEAKER = "m 0 10 l 10 10 l 22 0 l 22 30 l 10 20 l 0 20"
-- The muted state draws this slash across the speaker, so one element
-- carries both the level and the mute. The slash takes a dark outline
-- because it draws in the speaker's own color, and the two would read as
-- one shape without it.
local SLASH = "m 2 24 l 24 2 l 24 8 l 2 30"
local SLASH_BORDER = 2

-- The last values the observers reported.
local level = nil
local muted = false

-- The indicator's own fade factor, the target it steps toward, the timer
-- that steps it, and the timer that starts the fade out. The OSD runs the
-- same two clocks at the same rates, and neither one reads the other, so a
-- level change shows the level alone and a summoned OSD shows no level.
local fade = 0
local fade_target = 0
local fade_timer = nil
local hide_timer = nil

-- fade_step moves the factor toward its target on each tick, at the in
-- rate on the way up and the out rate on the way down, then redraws. The
-- timer stops at the target.
local function fade_step()
  local rate_ms = theme.fade_out_ms
  if fade_target > fade then
    rate_ms = theme.fade_in_ms
  end
  local step = theme.fade_tick * 1000 / rate_ms
  if fade_target > fade then
    fade = math.min(fade_target, fade + step)
  else
    fade = math.max(fade_target, fade - step)
  end
  redraw_cb()
  if fade == fade_target and fade_timer then
    fade_timer:kill()
    fade_timer = nil
  end
end

-- start_fade sets the target and runs the timer until the factor reaches
-- it. One timer serves both directions, so a change during a fade out
-- reverses the same timer in place.
local function start_fade(target)
  fade_target = target
  if fade == fade_target then
    return
  end
  if not fade_timer then
    fade_timer = mp.add_periodic_timer(theme.fade_tick, fade_step)
  end
end

-- Each change restarts the wait, so a run of presses holds the indicator
-- on screen and the fade out starts from the last one.
local function arm_hide()
  if hide_timer then
    hide_timer:kill()
  end
  hide_timer = mp.add_timeout(theme.idle_hide, function()
    hide_timer = nil
    start_fade(0)
  end)
end

-- The command sidecar sends volume-changed after it applies a level from
-- the bus, and not for the retained value it reads when it first connects.
-- So the indicator answers a press, and it stays off screen while a pod
-- restores the level it starts with.
function volume.show()
  start_fade(1)
  arm_hide()
  redraw_cb()
end

-- on_volume records each value of mpv's volume property and shows nothing
-- on its own. It redraws while the indicator is on screen, so a level that
-- lands after the sidecar's message reaches the bar it belongs to.
function volume.on_volume(value)
  if type(value) ~= "number" then
    return
  end
  level = value
  if fade > 0 then
    redraw_cb()
  end
end

-- on_mute records the muted flag the way on_volume records the level.
function volume.on_mute(value)
  muted = value == true
  if fade > 0 then
    redraw_cb()
  end
end

-- draw returns the row, or nil while the indicator is off screen. The
-- glyph alone carries the muted state, so the bar reads the level in both
-- states. The row draws at its own fade: it sets the factor theme scales
-- every alpha by, and it puts back the factor the caller drew the rest of
-- the frame at.
function volume.draw()
  if fade <= 0 or not level then
    return nil
  end
  local outer_fade = theme.fade
  theme.set_fade(fade)
  local col = columns()
  local parts = {}
  parts[#parts + 1] = theme.rounded_rect(
    col.surface_x, SURFACE_Y, col.surface_w, SURFACE_H, SURFACE_R, theme.color.shadow,
    string.format("&H%02X&", theme.scrim_edge_alpha)
  )
  parts[#parts + 1] = theme.rounded_rect(
    col.bar_x, BAR_TOP, BAR_W, BAR_H, BAR_R, theme.color.track, theme.alpha.track
  )
  local fillw = BAR_W * math.max(0, math.min(1, level / FULL))
  if fillw >= 1 then
    parts[#parts + 1] = theme.rounded_rect(
      col.bar_x, BAR_TOP, fillw, BAR_H, BAR_R, theme.color.fill, theme.alpha.opaque
    )
  end
  local glyph_color = theme.color.text
  if muted then
    glyph_color = theme.color.muted
  end
  parts[#parts + 1] = theme.shape(col.glyph_x, GLYPH_TOP, SPEAKER, glyph_color, theme.alpha.opaque)
  if muted then
    parts[#parts + 1] = theme.shape(
      col.glyph_x, GLYPH_TOP, SLASH, glyph_color, theme.alpha.opaque,
      SLASH_BORDER, theme.color.shadow
    )
  end
  parts[#parts + 1] = theme.text(
    right(), ROW_Y, string.format("%d", math.floor(level + 0.5)),
    theme.type.small, theme.color.text, 9, theme.alpha.opaque
  )
  theme.set_fade(outer_fade)
  return table.concat(parts, "\n")
end

return volume
