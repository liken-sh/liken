-- The identity block, the bottom-left element of the idle screen. It names the
-- unit and lists its parts, so a person reads what the unit is and what it
-- plays through while no film runs. The operator passes the name and the parts
-- in the environment: the name always, and the parts only when the Player lists
-- any. So an unnamed Player still shows a name, and a Player with no listed
-- parts shows the name alone over the clock.
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

-- identity.draw returns the header and one line per part, all flush at the same
-- left edge, anchored so the last part sits at BOTTOM_Y and the block grows
-- upward from there. It returns nil when the environment names no unit, so the
-- idle screen draws the clock alone. The an1 alignment anchors each line at its
-- bottom-left, so a line's position is its own bottom and the stack builds from
-- the bottom up. The name and the parts read at full brightness, the way the
-- rest of the idle draw does.
function identity.draw()
  local name = os.getenv("IDLE_PLAYER_NAME")
  if not name or name == "" then
    return nil
  end
  local items = split_lines(os.getenv("IDLE_PLAYER_COMPONENTS"))

  local parts = {}
  local y = BOTTOM_Y
  for index = #items, 1, -1 do
    parts[#parts + 1] = theme.text(
      LEFT, y, items[index], ITEM_SIZE, theme.color.text, 1, theme.alpha.opaque
    )
    if index > 1 then
      y = y - math.floor(ITEM_SIZE * ITEM_LEADING + 0.5)
    end
  end
  if #items > 0 then
    y = y - math.floor(ITEM_SIZE * HEADER_LEADING + 0.5)
  end
  parts[#parts + 1] = theme.text(LEFT, y, name, HEADER_SIZE, theme.color.text, 1, theme.alpha.opaque)
  return table.concat(parts, "\n")
end

return identity
