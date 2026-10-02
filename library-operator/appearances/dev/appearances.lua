-- The overlay for `appearances review --play`. It draws each detected face
-- of the current shot as a box over the video, labeled with the closest
-- person in the gallery and the similarity, and prints the current span's
-- people in the top left corner. The spans are also mpv's chapters, so the
-- seek bar shows a tick at each span and Page Up and Page Down move between
-- spans.
--
-- A green box is a named face. A yellow box is an unnamed face whose closest
-- person is within 0.1 of the threshold, or over the threshold but too
-- close to the next person to pass the margin. A threshold or a margin
-- change moves these faces. A red box is a face nobody in the gallery is
-- close to.
--
-- Keys: k and j seek to the next and the previous sample with a face, at
-- the sample's exact time, where each box sits on its face. v shows and
-- hides the overlay.

-- mpv runs Lua 5.1 or LuaJIT, so this script uses no integer division and
-- no other syntax from later versions.

local utils = require("mp.utils")

local path = os.getenv("APPEARANCES_REVIEW")
if not path then
    mp.msg.error("APPEARANCES_REVIEW names no review file")
    return
end
local file = assert(io.open(path, "r"))
local data = utils.parse_json(file:read("*a"))
file:close()

local overlay = mp.create_osd_overlay("ass-events")
local visible = true

-- The last entry of a list sorted by `key` that starts at or before t.
local function last_before(list, key, t)
    local low, high, found = 1, #list, nil
    while low <= high do
        local middle = math.floor((low + high) / 2)
        if list[middle][key] <= t then
            found = middle
            low = middle + 1
        else
            high = middle - 1
        end
    end
    return found
end

local function escape(text)
    return (text:gsub("\\", "\\\\"):gsub("{", "\\{"):gsub("}", "\\}"))
end

local function clock(seconds)
    return string.format("%d:%02d:%04.1f", math.floor(seconds / 3600), math.floor(seconds % 3600 / 60), seconds % 60)
end

-- ASS colors are blue, green, red.
local GREEN, YELLOW, RED = "&H40D040&", "&H30D0F0&", "&H4040E0&"

local function color(face)
    if face.named then
        return GREEN
    elseif face.similarity >= data.threshold - 0.1 then
        return YELLOW
    end
    return RED
end

-- After an exact seek to a sample, mpv's time-pos can read a fraction of
-- a millisecond before the sample's time, which would select the sample
-- before it. Every lookup adds a tolerance, well under one frame.
local TOLERANCE = 0.01

local function now()
    local t = mp.get_property_number("time-pos")
    return t and t + TOLERANCE
end

local function draw()
    local t = now()
    local dim = mp.get_property_native("osd-dimensions")
    if not visible or not t or not dim or dim.w == 0 then
        overlay:remove()
        return
    end
    -- The video's rectangle inside the window, from the margins mpv
    -- leaves for letterboxing.
    local sx = (dim.w - dim.ml - dim.mr) / data.width
    local sy = (dim.h - dim.mt - dim.mb) / data.height
    local lines = {}

    local k = last_before(data.samples, "time", t)
    if k then
        local sample = data.samples[k]
        -- The boxes are exact only at the sample. Later the faces move, so
        -- the outline thins.
        local border = (t - sample.time < 0.5) and 3 or 1
        for _, face in ipairs(sample.faces) do
            local x1, y1 = dim.ml + face.x * sx, dim.mt + face.y * sy
            local x2, y2 = x1 + face.w * sx, y1 + face.h * sy
            local c = color(face)
            lines[#lines + 1] = string.format(
                "{\\an7\\pos(0,0)\\bord%d\\shad0\\1a&HFF&\\3c%s\\p1}m %d %d l %d %d %d %d %d %d{\\p0}",
                border, c, x1, y1, x2, y1, x2, y2, x1, y2)
            local label = face.person == "" and "?" or face.person
            if not face.named then
                label = "? " .. label
            end
            lines[#lines + 1] = string.format(
                "{\\an1\\pos(%d,%d)\\fs22\\bord2\\shad0\\1c%s}%s %.2f",
                x1, y1 - 4, c, escape(label), face.similarity)
        end
    end

    local s = last_before(data.spans, "start", t)
    local header = "nobody named"
    if s then
        local span = data.spans[s]
        local people = #span.people > 0 and table.concat(span.people, ", ") or "nobody named"
        header = string.format("%s to %s   %s", clock(span.start), clock(span["end"]), people)
    end
    local sample = k and string.format("   sample %d of %d at %s", k, #data.samples,
        clock(data.samples[k].time)) or ""
    lines[#lines + 1] = string.format("{\\an7\\pos(20,20)\\fs26\\bord2\\shad0}%s{\\fs18}\\N%s   threshold %.3f   margin %.3f",
        escape(header), sample, data.threshold, data.margin)

    overlay.res_x, overlay.res_y = dim.w, dim.h
    overlay.data = table.concat(lines, "\n")
    overlay:update()
end

local function seek_face(step)
    local t = now() or 0
    local k = last_before(data.samples, "time", t) or 0
    -- Between two samples, j goes back to the one at or before the
    -- current time, not the one before that.
    if step < 0 and k > 0 and data.samples[k].time < t - 2 * TOLERANCE then
        k = k + 1
    end
    local i = k + step
    while i >= 1 and i <= #data.samples do
        if #data.samples[i].faces > 0 then
            mp.commandv("seek", tostring(data.samples[i].time), "absolute+exact")
            return
        end
        i = i + step
    end
end

mp.add_key_binding("k", "appearances-next-face", function() seek_face(1) end)
mp.add_key_binding("j", "appearances-previous-face", function() seek_face(-1) end)
mp.add_key_binding("v", "appearances-toggle", function()
    visible = not visible
    draw()
end)
mp.observe_property("time-pos", "number", draw)
mp.observe_property("osd-dimensions", "native", draw)
