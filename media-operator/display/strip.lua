-- The control strip, the lowest region. It lays the controls out in groups, a
-- heading over each track and its delay offset, moves the horizontal focus
-- across the controls present for this file, and asks the focused control to act
-- on select. A control that opens a chooser returns itself, and the strip hands
-- that back to focus as the capturing module.
local theme = require("theme")
local audio = require("audio")
local subtitles = require("subtitles")
local video = require("video")
local offset = require("offset")

local strip = {}

-- The row sits at the bottom right, its right edge at the margin.
local RIGHT = theme.canvas.w - theme.margin.x
-- The heading row and the value row, in canvas pixels. The heading sits close
-- above its value, and the pair sits clear below the scrubber's time line.
local Y_HEAD = 990
local Y_VAL = 1016
-- Within a group, the offset value sits this far right of the track value.
local MEMBER_W = 200
-- The pitch between two groups, and the nominal ink width of one group. The
-- strip right-aligns the row on the nominal, so the row holds its place when a
-- value changes width.
local GROUP_PITCH = 430
local GROUP_INK = 260

local audio_offset = offset.new({ prop = "audio-delay", label = "Audio offset" })
local sub_offset = offset.new({ prop = "sub-delay", label = "Subtitle offset", track = "sub" })

-- The flat focus order the left and right presses walk.
local controls = { audio, audio_offset, subtitles, sub_offset, video }
-- The layout groups, a heading over a track and its offset. The offsets carry
-- no heading of their own, because they read under the group's heading.
local groups = {
  { heading = "audio", members = { audio, audio_offset } },
  { heading = "subtitles", members = { subtitles, sub_offset } },
  { heading = "video", members = { video } },
}
local index = 1

function strip.set_redraw(fn)
  for _, c in ipairs(controls) do
    c.set_redraw(fn)
  end
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

-- The present groups, each with its available members. A group with no present
-- member is left out, so a file with no subtitles draws no subtitle group.
local function present_groups()
  local out = {}
  for _, g in ipairs(groups) do
    local members = {}
    for _, m in ipairs(g.members) do
      if m.available() then
        members[#members + 1] = m
      end
    end
    if #members > 0 then
      out[#out + 1] = { heading = g.heading, members = members }
    end
  end
  return out
end

function strip.draw(focused)
  local flat = present()
  if #flat == 0 then
    return nil
  end
  clamp(flat)
  local here = focused and flat[index] or nil
  local pg = present_groups()
  -- Right-align the row of groups. The last group's nominal ink ends at the
  -- margin, so the row holds its right edge as a value changes width.
  local start = RIGHT - GROUP_INK - (#pg - 1) * GROUP_PITCH
  local parts = {}
  for gi, g in ipairs(pg) do
    local gx = start + (gi - 1) * GROUP_PITCH
    parts[#parts + 1] = theme.text(gx, Y_HEAD, g.heading, theme.type.tiny, theme.color.muted, 4, theme.alpha.subdued)
    for mi, m in ipairs(g.members) do
      local s = m.draw(gx + (mi - 1) * MEMBER_W, Y_VAL, m == here)
      if s then
        parts[#parts + 1] = s
      end
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
