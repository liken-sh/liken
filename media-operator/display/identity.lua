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
local theme = require("theme")

local identity = {}

local LEFT = theme.margin.x
-- The block sits the same distance from the bottom edge the clock sits from
-- the top, so the two balance across the screen. The clock holds the
-- top-right and the block holds the bottom-left, so the two never overlap.
local BOTTOM_Y = theme.canvas.h - 90

-- The header reads one step larger than the parts, so the unit's name leads the
-- list. The parts sit close together, and the header stands off from the first
-- part by a wider gap, so the name reads as the title of the list below it.
local HEADER_SIZE = theme.type.label
-- Four points under theme.type.small, so the parts read a touch lighter than
-- the shared small size without changing that size for every other element.
local ITEM_SIZE = theme.type.small - 4
local ITEM_LEADING = 1.1
local HEADER_LEADING = 1.3

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

-- The three channel bytes of the text color, so the flash interpolates from the
-- normal text color toward white and back.
local TEXT_B, TEXT_G, TEXT_R = theme.color.text:match("&H(%x%x)(%x%x)(%x%x)&")

local redraw_cb = function() end
function identity.set_redraw(fn)
  redraw_cb = fn
end

-- The parts arrive as one string with a newline between each name. Split it
-- into a list, and return an empty list for an empty or absent value.
local function split_lines(text)
  local lines = {}
  if not text or text == "" then
    return lines
  end
  for line in (text .. "\n"):gmatch("(.-)\n") do
    lines[#lines + 1] = line
  end
  return lines
end

-- One entry per part: its name, whether it reports a live connection, the
-- brightness it draws at now, the brightness it eases toward, and the flash it
-- carries from a reconnection. connected is nil for a part that reports no live
-- state at all.
local function new_item(name, connected)
  local lit = connected ~= false
  return {
    name = name,
    connected = connected,
    level = lit and 1 or 0,
    target = lit and 1 or 0,
    flash = 0,
    flash_rising = false,
  }
end

local header = os.getenv("IDLE_PLAYER_NAME")
local items = {}
for _, name in ipairs(split_lines(os.getenv("IDLE_PLAYER_COMPONENTS"))) do
  items[#items + 1] = new_item(name, nil)
end

local timer = nil

-- Step every part toward its target brightness, then run any flash through its
-- rise and its fall, and stop the timer once nothing moves. One timer serves
-- every part and both directions, so a part that reconnects during its own
-- fade-out turns around on the same timer.
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
    if item.flash_rising then
      item.flash = item.flash + rise_step
      if item.flash >= 1 then
        item.flash = 1
        item.flash_rising = false
      end
      moving = true
    elseif item.flash > 0 then
      item.flash = math.max(0, item.flash - fall_step)
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
-- that tells a person the controller came back.
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
        local item = previous[component.name]
        if item then
          local returned = connected == true and item.connected == false
          item.connected = connected
          item.target = connected ~= false and 1 or 0
          if returned then
            item.flash_rising = true
          end
        else
          item = new_item(component.name, connected)
        end
        list[#list + 1] = item
      end
    end
  end
  items = list
  start_timer()
  redraw_cb()
end

-- The alpha one part draws at. level 1 is opaque and level 0 is theme's dim.
local function item_alpha(level)
  return string.format("&H%02X&", math.floor(DIM_BYTE * (1 - level) + 0.5))
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
      LEFT, y, item.name, ITEM_SIZE, item_color(item.flash), 1, item_alpha(item.level)
    )
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
