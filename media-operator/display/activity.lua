-- The activity line, the one line the idle screen draws while a Play starts or
-- runs. It sits at the top right under the clock and names the title, so the
-- seconds a playback pod pulls and starts read as work in progress and not as a
-- screen that ignored the button.
--
-- The line holds the last title it was given after the Play ends, because it
-- fades out with the mark's energy rather than vanishing on the frame the
-- activity changes. At rest the energy is 0 and the line draws nothing.
local theme = require("theme")

local activity = {}

-- The line shares the clock's right edge and its top, so the two stack in the
-- same column. LINE_Y clears the clock's own line: the clock hangs from the
-- top margin at theme.type.small, and the gap below it keeps the two lines
-- apart without touching.
local RIGHT = theme.canvas.w - theme.margin.x
local LINE_Y = theme.margin.y + theme.type.small + 12

-- The typographic marks, as UTF-8 bytes. \226\128\156 and \226\128\157 are the
-- left and right double quotation marks, and \226\128\166 is the ellipsis. The
-- title takes real quotation marks because the line is read at a distance, and
-- the ellipsis is one character so it never wraps or spaces oddly.
local OPEN_QUOTE = "\226\128\156"
local CLOSE_QUOTE = "\226\128\157"
local ELLIPSIS = "\226\128\166"

local kind = nil
local title = nil

-- receive takes the Player's retained status as a parsed table. The title comes
-- from the Play block, which the operator resolves from the Presentation, so the
-- display formats one string and reads no series or episode fields of its own.
-- A status with no Play leaves the last title in place for the fade-out.
function activity.receive(status)
  if type(status) ~= "table" then
    return
  end
  kind = status.activity
  if type(status.play) == "table" then
    if type(status.play.title) == "string" and status.play.title ~= "" then
      title = status.play.title
    elseif type(status.play.name) == "string" and status.play.name ~= "" then
      title = status.play.name
    end
  end
end

-- activity.draw returns the line, or nil when there is nothing to say. level is
-- the energy. While a Play starts or runs the line draws opaque, whatever the
-- energy is. After that the line draws at the energy, so the ramp-down that
-- follows a reveal carries the line out with the mark's motion, and a settled
-- idle screen at energy 0 draws no line at all.
function activity.draw(level)
  if not title then
    return nil
  end
  local alpha = theme.alpha.opaque
  if kind ~= "Starting" and kind ~= "Playing" then
    if not level or level <= 0 then
      return nil
    end
    alpha = string.format("&H%02X&", math.floor(255 * (1 - level) + 0.5))
  end
  local text = "Playing " .. OPEN_QUOTE .. title .. CLOSE_QUOTE .. ELLIPSIS
  return theme.text(RIGHT, LINE_Y, text, theme.type.small, theme.color.text, 9, alpha)
end

return activity
