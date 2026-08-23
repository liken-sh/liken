-- The control strip, the lowest region. It holds the controls, moves the
-- horizontal focus across the ones present for this file, and asks the
-- focused control to act on select. A control that opens a chooser returns
-- itself, and the strip hands that back to focus as the capturing module.
local theme = require("theme")
local audio = require("audio")
local subtitles = require("subtitles")

local strip = {}

local LEFT = theme.margin.x
local Y = 970
-- The spacing between two control cells, wide enough for a track label.
local CELL_GAP = 560

local controls = { audio, subtitles }
local index = 1

function strip.set_redraw(fn)
  audio.set_redraw(fn)
  subtitles.set_redraw(fn)
end

local function present()
  local out = {}
  for _, c in ipairs(controls) do
    if c.available() then
      out[#out + 1] = c
    end
  end
  return out
end

function strip.available()
  return #present() > 0
end

local function clamp(p)
  if index > #p then
    index = #p
  end
  if index < 1 then
    index = 1
  end
end

function strip.draw(focused)
  local p = present()
  if #p == 0 then
    return nil
  end
  clamp(p)
  local parts = {}
  for i, c in ipairs(p) do
    local x = LEFT + (i - 1) * CELL_GAP
    local s = c.draw(x, Y, focused and i == index)
    if s then
      parts[#parts + 1] = s
    end
  end
  return table.concat(parts, "\n")
end

function strip.press(action)
  local p = present()
  if #p == 0 then
    return nil
  end
  clamp(p)
  if action == "left" then
    index = math.max(1, index - 1)
  elseif action == "right" then
    index = math.min(#p, index + 1)
  elseif action == "select" then
    return p[index].activate()
  end
  return nil
end

return strip
