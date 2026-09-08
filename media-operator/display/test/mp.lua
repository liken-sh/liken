-- The fake mp table the display's tests run against. mpv is the only host
-- that defines the mp global. This one records the commands a module sends and
-- holds the timeouts it arms, so a test reads what the display did and fires a
-- timeout without a wait.

local harness = {}

-- One fake mp table per test, so no test reads another test's commands.
function harness.new()
  local fake = { commands = {}, timeouts = {} }

  -- mpv runs the command. This records it, in order.
  function fake.command_native(command)
    fake.commands[#fake.commands + 1] = command
    return true
  end

  -- The handle mpv returns carries kill, and the display kills a timer it no
  -- longer needs. There is no clock here, so the fake records the kill.
  function fake.add_timeout(seconds, fn)
    local handle = { seconds = seconds, fn = fn, killed = false, fired = false }
    function handle:kill()
      self.killed = true
    end
    fake.timeouts[#fake.timeouts + 1] = handle
    return handle
  end

  -- Run the newest timeout that is neither killed nor fired, which is what
  -- mpv's event loop does when the seconds pass with no reply.
  function fake.fire_timeout()
    for i = #fake.timeouts, 1, -1 do
      local handle = fake.timeouts[i]
      if not handle.killed and not handle.fired then
        handle.fired = true
        handle.fn()
        return handle
      end
    end
    return nil
  end

  return fake
end

return harness
