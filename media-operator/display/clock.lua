-- The clock, the top-right element. It reads the wall-clock time and the time
-- the film ends, now plus the time left, so a viewer reads the hour without
-- leaving the film. The current time reads bright, and the end time reads dim.
--
-- The reading holds one box. It hangs from the top margin and ends on the side
-- margin, whether or not the film has a duration. The idle screen and the
-- library browser draw their own clock in the same box. The three screens
-- follow each other on one panel, and a reading that moved would make the hour
-- jump when one screen replaces another. The end time therefore draws on the
-- line under the reading, where the idle screen draws its activity line. An end
-- time beside the reading would push the reading left by its own width.
local theme = require("theme")

local clock = {}

local function right()
  return theme.canvas.w - theme.margin.x
end
local TOP_Y = theme.margin.y
local ENDS_Y = theme.margin.y + theme.line_pitch

-- Format a wall-clock time as "3:01 pm", a twelve-hour clock with no leading
-- zero and a lowercase suffix.
local function fmt(t)
  local hour = tonumber(os.date("%H", t))
  local minute = os.date("%M", t)
  local suffix = hour < 12 and "am" or "pm"
  local twelve = hour % 12
  if twelve == 0 then
    twelve = 12
  end
  return string.format("%d:%s %s", twelve, minute, suffix)
end

-- clock.draw returns the top-right time. With a duration it adds the end time,
-- now plus the time left, on the line under it. A paused film reads the hour it
-- would end from where it sits, which is close enough to read at a glance. The
-- current time draws bright and the end time draws dim, so the two tell apart.
function clock.draw()
  local now = os.time()
  local reading = theme.text(
    right(), TOP_Y, fmt(now), theme.type.small, theme.color.text, 9, theme.alpha.opaque
  )
  local duration = mp.get_property_number("duration")
  local pos = mp.get_property_number("time-pos")
  if not (duration and pos and duration > 0) then
    return reading
  end
  local ends = "ends " .. fmt(now + math.floor(duration - pos + 0.5))
  return reading
    .. "\n"
    .. theme.text(right(), ENDS_Y, ends, theme.type.small, theme.color.text, 9, theme.alpha.subdued)
end

return clock
