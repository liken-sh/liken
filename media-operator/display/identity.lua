-- The identity block, the bottom-left element of the idle screen. It names the
-- unit and lists its parts, so a person reads what the unit is and what it
-- plays through while no film runs.
--
-- The block has two sources, in that order. The operator sets the name and the
-- parts in the environment when it creates the idle pod, and the block seeds
-- from those values so the first frame is never blank and the local preview
-- needs no broker. The Player's retained status then replaces the whole set,
-- name and parts together, so an edit to a Player shows with no pod restart.
--
-- Each part carries its own brightness. A part with live state, a controller
-- that connects and disconnects, draws dim while it is away and pulses back to
-- full when it returns. A part with no live state, a wired screen or its
-- built-in speakers, draws at full brightness always, because a part that cannot
-- be absent must not read as present-for-now.
--
-- The block also shows focus. The status marks at most one part focused,
-- the remote whose presses drive this unit, and that line carries a small
-- hexagon in the left margin. The environment seed carries no focus, so no
-- hexagon draws before the first status. When focus arrives here, the
-- sidecar sends focus-pulse with the remote's 0-based index, and that
-- line's hexagon beats white once.
local theme = require("theme")

local identity = {}

local LEFT = theme.margin.x
-- The block sits the same distance from the bottom edge the clock sits from
-- the top, so the two balance across the screen. The clock holds the
-- top-right and the block holds the bottom-left, so the two never overlap.
local BOTTOM_Y = theme.canvas.h - theme.margin.y

-- The header reads one step larger than the parts, so the unit's name leads the
-- list. The parts sit close together, and the header stands off from the first
-- part by a wider gap, so the name reads as the title of the list below it.
local HEADER_SIZE = theme.type.label
-- Four points under theme.type.small, so the parts read a touch lighter than
-- the shared small size without changing that size for every other element.
local ITEM_SIZE = theme.type.small - 4
local ITEM_LEADING = 1.1
local HEADER_LEADING = 1.3

-- The focus marker's geometry: a hexagon, the same shape as the logo mark.
-- MARKER_R is center-to-vertex, a fifth of ITEM_SIZE, so the mark scales
-- with the line and stands 12 canvas pixels tall beside a 30 pixel name.
-- MARKER_X is the mark's center, MARKER_GAP left of LEFT, inside the
-- margin, so the names keep one flush-left column with or without a mark.
-- MARKER_RISE lifts the center off the line's an1 anchor, the bottom of
-- the line box, to the middle of the lowercase letters.
local MARKER_R = ITEM_SIZE / 5
local MARKER_GAP = 18
local MARKER_X = LEFT - MARKER_GAP
local MARKER_RISE = ITEM_SIZE * 0.42

-- The brightness of one part runs from 0, theme's dim alpha, to 1, opaque. A
-- disconnected part settles at 0 and a connected part at 1. The step runs on the
-- same period the OSD fade uses, about sixty times a second.
local TICK = 1 / 60
local DIM_MS = 400
-- The flash rises quickly and settles slowly, so a reconnection reads as one
-- bright beat and not as a blink.
local FLASH_RISE_MS = 120
local FLASH_FALL_MS = 500

-- The dim end of the range, read from theme's vocabulary so the palette stays in
-- one file. theme.alpha.dim is 0xA8 of 0xFF transparent, which leaves about a
-- third of full brightness: enough to read the name, far enough from full that a
-- glance tells the disconnected part from the rest.
local DIM_BYTE = tonumber(theme.alpha.dim:match("&H(%x+)&"), 16)

-- The marker's two resting alphas. On a lit line the mark draws at
-- theme.alpha.dim, one step under its name. On a dim line it draws at dim
-- of dim, 0xE1, the same fraction of the line it holds on a lit one, so
-- the mark never reads brighter than the part it marks. A pulse carries it
-- to opaque.
local MARKER_BYTE = DIM_BYTE
local MARKER_DIM_BYTE = 255 - math.floor((255 - DIM_BYTE) * (255 - DIM_BYTE) / 255 + 0.5)

-- The three channel bytes of the text color, so the flash interpolates from the
-- normal text color toward white and back.
local TEXT_B, TEXT_G, TEXT_R = theme.color.text:match("&H(%x%x)(%x%x)(%x%x)&")

local redraw_cb = function() end
function identity.set_redraw(fn)
  redraw_cb = fn
end

-- The parts arrive in the environment as one string with a newline between
-- each name. Split it into a list, and return an empty list for an empty or
-- absent value. The function is exported because that environment format is
-- this block's contract, and the preview builds its fake status from the
-- same seed.
function identity.split_lines(text)
  local lines = {}
  if not text or text == "" then
    return lines
  end
  for line in (text .. "\n"):gmatch("(.-)\n") do
    lines[#lines + 1] = line
  end
  return lines
end
local split_lines = identity.split_lines

-- A beat is one white pulse: level 0 at rest, rising while it climbs to 1,
-- then falling back to 0. Two beats run per part, the reconnection flash
-- on the name and the focus pulse on the marker, and both use these two
-- fields so one stepper serves them.
local function new_beat()
  return { level = 0, rising = false }
end

-- One entry per part: its name, its kind, its presence, its focus, the
-- brightness it draws at now and eases toward, and its two beats. kind is
-- the component's kind from the status, "remote" on a controller, and nil
-- for a part the environment seeded. connected is nil for a part that
-- reports no live state. focused is true only on the part the status
-- marks, and nil before the first status.
local function new_item(name, kind, connected, focused)
  local lit = connected ~= false
  return {
    name = name,
    kind = kind,
    connected = connected,
    focused = focused,
    level = lit and 1 or 0,
    target = lit and 1 or 0,
    flash = new_beat(),
    pulse = new_beat(),
  }
end

local header = os.getenv("IDLE_PLAYER_NAME")
local items = {}
for _, name in ipairs(split_lines(os.getenv("IDLE_PLAYER_COMPONENTS"))) do
  items[#items + 1] = new_item(name, nil, nil, nil)
end

local timer = nil

-- step_beat carries one beat through its rise and its fall, and reports
-- whether the beat still moves.
local function step_beat(beat, rise_step, fall_step)
  if beat.rising then
    beat.level = beat.level + rise_step
    if beat.level >= 1 then
      beat.level = 1
      beat.rising = false
    end
    return true
  end
  if beat.level > 0 then
    beat.level = math.max(0, beat.level - fall_step)
    return true
  end
  return false
end

-- Step every part toward its target brightness, then run its flash and its
-- focus pulse through their rise and fall, and stop the timer once nothing
-- moves. One timer serves every part, both beats, and both directions, so
-- a part that reconnects during its own fade-out turns around on the same
-- timer.
local function step()
  local moving = false
  local dim_step = TICK * 1000 / DIM_MS
  local rise_step = TICK * 1000 / FLASH_RISE_MS
  local fall_step = TICK * 1000 / FLASH_FALL_MS
  for _, item in ipairs(items) do
    if item.level < item.target then
      item.level = math.min(item.target, item.level + dim_step)
    elseif item.level > item.target then
      item.level = math.max(item.target, item.level - dim_step)
    end
    if item.level ~= item.target then
      moving = true
    end
    if step_beat(item.flash, rise_step, fall_step) then
      moving = true
    end
    if step_beat(item.pulse, rise_step, fall_step) then
      moving = true
    end
  end
  redraw_cb()
  if not moving and timer then
    timer:kill()
    timer = nil
  end
end

local function start_timer()
  if not timer then
    timer = mp.add_periodic_timer(TICK, step)
  end
end

-- receive takes the Player's retained status as a parsed table and replaces the
-- whole block with it. A part that keeps its name keeps the brightness it draws
-- at, so a status that changed one field does not restart every fade. A part
-- that changed from disconnected to connected starts a flash, which is the pulse
-- that tells a person the controller came back. The status sets only
-- whether the focus marker draws. A part that gained focused starts no
-- beat, because the sidecar sends focus-pulse for the arrival and the
-- status carries no timing of its own.
function identity.receive(status)
  if type(status) ~= "table" then
    return
  end
  if type(status.displayName) == "string" and status.displayName ~= "" then
    header = status.displayName
  end
  local previous = {}
  for _, item in ipairs(items) do
    previous[item.name] = item
  end
  local list = {}
  if type(status.components) == "table" then
    for _, component in ipairs(status.components) do
      if type(component) == "table" and type(component.name) == "string" and component.name ~= "" then
        local connected = nil
        if type(component.connected) == "boolean" then
          connected = component.connected
        end
        local kind = nil
        if type(component.kind) == "string" then
          kind = component.kind
        end
        -- focused is absent on every part but the focused one, so its
        -- absence is a false and never an unknown.
        local focused = component.focused == true
        local item = previous[component.name]
        if item then
          local returned = connected == true and item.connected == false
          item.kind = kind
          item.connected = connected
          item.focused = focused
          item.target = connected ~= false and 1 or 0
          if returned then
            item.flash.rising = true
          end
        else
          item = new_item(component.name, kind, connected, focused)
        end
        list[#list + 1] = item
      end
    end
  end
  items = list
  start_timer()
  redraw_cb()
end

-- identity.pulse beats one part's marker white once. The index is the
-- remote's 0-based position as the sidecar sends it, counted over the
-- parts of kind "remote" in the order the status lists them, which is the
-- Player's spec.remotes order. An index that names no remote changes
-- nothing, because a pulse can arrive for a remote this unit's status has
-- not listed yet. The beat runs whether or not the marker draws now, so a
-- pulse that lands just before the status that sets focused still shows.
function identity.pulse(index)
  index = tonumber(index)
  if not index or index < 0 or index ~= math.floor(index) then
    return
  end
  local remaining = index
  for _, item in ipairs(items) do
    if item.kind == "remote" then
      if remaining == 0 then
        item.pulse.rising = true
        start_timer()
        redraw_cb()
        return
      end
      remaining = remaining - 1
    end
  end
end

-- The alpha one part draws at. level 1 is opaque and level 0 is theme's dim.
local function item_alpha(level)
  return string.format("&H%02X&", math.floor(DIM_BYTE * (1 - level) + 0.5))
end

-- The marker's alpha at rest runs with the line's own brightness, from
-- theme's dim on a lit line to dim of dim on a disconnected one, so the
-- mark carries the same news the name does. The pulse then lifts it to
-- opaque, and that lift is what a person sees when focus arrives.
local function marker_alpha(level, pulse)
  local rest = MARKER_BYTE + (MARKER_DIM_BYTE - MARKER_BYTE) * (1 - level)
  return string.format("&H%02X&", math.floor(rest * (1 - pulse) + 0.5))
end

-- The color one part draws in. At flash 0 it is the normal text color, and at
-- flash 1 every channel reaches 0xFF, the white beat of a reconnection.
local function item_color(flash)
  if flash <= 0 then
    return theme.color.text
  end
  local function lift(byte)
    local v = tonumber(byte, 16)
    return math.floor(v + (0xFF - v) * flash + 0.5)
  end
  return string.format("&H%02X%02X%02X&", lift(TEXT_B), lift(TEXT_G), lift(TEXT_R))
end

-- identity.draw returns the header and one line per part, all flush at the same
-- left edge, anchored so the last part sits at BOTTOM_Y and the block grows
-- upward from there. It returns nil when nothing names the unit, so the idle
-- screen draws the clock alone. The an1 alignment anchors each line at its
-- bottom-left, so a line's position is its own bottom and the stack builds from
-- the bottom up. The name reads at full brightness, and each part reads at its
-- own.
function identity.draw()
  if not header or header == "" then
    return nil
  end

  local parts = {}
  local y = BOTTOM_Y
  for index = #items, 1, -1 do
    local item = items[index]
    parts[#parts + 1] = theme.text(
      LEFT, y, item.name, ITEM_SIZE, item_color(item.flash.level), 1, item_alpha(item.level)
    )
    -- The marker draws for the focused part, and for a part mid-pulse
    -- whose status has not landed yet. theme.hexagon takes the top-left of
    -- the mark's box, so the center subtracts MARKER_R on both axes. The
    -- mark takes the same color the name does, so a reconnection's flash
    -- carries both.
    if item.focused or item.pulse.level > 0 then
      parts[#parts + 1] = theme.hexagon(
        MARKER_X - MARKER_R, y - MARKER_RISE - MARKER_R, MARKER_R,
        item_color(math.max(item.flash.level, item.pulse.level)),
        marker_alpha(item.level, item.pulse.level)
      )
    end
    if index > 1 then
      y = y - math.floor(ITEM_SIZE * ITEM_LEADING + 0.5)
    end
  end
  if #items > 0 then
    y = y - math.floor(ITEM_SIZE * HEADER_LEADING + 0.5)
  end
  parts[#parts + 1] = theme.text(LEFT, y, header, HEADER_SIZE, theme.color.text, 1, theme.alpha.opaque)
  return table.concat(parts, "\n")
end

return identity
