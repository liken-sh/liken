-- The clock, the top-right element. It reads the wall-clock time and the time
-- the film ends, now plus the time left, so a viewer reads the hour without
-- leaving the film. The current time reads bright, and the end time reads dim.
local theme = require("theme")

local clock = {}

local function right()
  return theme.canvas.w - theme.margin.x
end
local TOP_Y = theme.margin.y

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
-- now plus the time left. A paused film reads the hour it would end from where
-- it sits, which is close enough to read at a glance. The current time draws
-- bright and the end time draws dim, so the two tell apart, and the dim override
-- runs to the end of the line.
function clock.draw()
  local now = os.time()
  local text = fmt(now)
  local duration = mp.get_property_number("duration")
  local pos = mp.get_property_number("time-pos")
  if duration and pos and duration > 0 then
    local ends = fmt(now + math.floor(duration - pos + 0.5))
    text = text .. "{\\1a" .. theme.faded_alpha(theme.alpha.subdued) .. "}" .. "  \194\183  ends " .. ends
  end
  return theme.text(right(), TOP_Y, text, theme.type.small, theme.color.text, 9, theme.alpha.opaque)
end

return clock
