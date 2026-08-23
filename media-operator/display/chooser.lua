-- The chooser panel. A control opens one to pick a track. It draws a vertical
-- list, bottom-anchored above the control strip, with the focused entry marked.
-- It is the one element that captures input, so it draws on top of every region.
local theme = require("theme")

local chooser = {}

local X = theme.margin.x
local W = 1180
local ROW_H = 58
local PAD = 24
-- The panel grows upward from this baseline, so it floats over the regions
-- above the strip.
local BOTTOM = 876
-- A subtitle list can run to dozens of tracks, more than fit on screen. The
-- panel shows a window of rows around the selection, so the current entry stays
-- in view however long the list.
local MAX_ROWS = 8
local MAX_CHARS = 56

local function clip(text)
  if #text > MAX_CHARS then
    return string.sub(text, 1, MAX_CHARS - 1) .. "\226\128\166"
  end
  return text
end

function chooser.draw(entries, sel)
  local n = #entries
  local visible = math.min(n, MAX_ROWS)
  local first = 1
  if n > MAX_ROWS then
    first = math.max(1, math.min(sel - math.floor(MAX_ROWS / 2), n - MAX_ROWS + 1))
  end

  local h = visible * ROW_H + PAD * 2
  local top = BOTTOM - h

  local parts = {}
  parts[#parts + 1] = theme.panel(X, top, W, h)

  local row0 = top + PAD
  for i = first, first + visible - 1 do
    local y = row0 + (i - first) * ROW_H
    local here = i == sel
    if here then
      parts[#parts + 1] = theme.rounded_rect(
        X + PAD / 2, y - 6, W - PAD, ROW_H - 4, 8, theme.color.fill, theme.alpha.highlight
      )
    end
    local color = here and theme.color.shadow or theme.color.muted
    parts[#parts + 1] = theme.text(X + PAD, y, clip(entries[i]), theme.type.label, color, 7)
  end

  return table.concat(parts, "\n")
end

return chooser
