// The FOCUSER mixin on a plain INDI::DefaultDevice, as a power box or a
// camera hosts it. INDI::Focuser adds Presets and Goto, which the mixin
// does not define. The mixin defines FOCUS_TIMER only for a focuser
// that cannot move to an absolute position, so the BARE_TIMER variant turns
// that one capability off.
#include <defaultdevice.h>
#include <indifocuserinterface.h>
#include <cstring>
#include <memory>

class BareFocuser : public INDI::DefaultDevice, public INDI::FocuserInterface
{
  public:
    BareFocuser() : INDI::FocuserInterface(this)
    {
        uint32_t capability = FOCUSER_CAN_ABS_MOVE | FOCUSER_CAN_REL_MOVE | FOCUSER_CAN_ABORT |
                              FOCUSER_CAN_REVERSE | FOCUSER_CAN_SYNC | FOCUSER_HAS_VARIABLE_SPEED |
                              FOCUSER_HAS_BACKLASH;
#ifdef BARE_TIMER
        capability &= ~FOCUSER_CAN_ABS_MOVE;
#endif
        FI::SetCapability(capability);
    }
    const char *getDefaultName() override { return "Bare Focuser"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        FI::initProperties(MAIN_CONTROL_TAB);
        setDriverInterface(FOCUSER_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        FI::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && FI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && FI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareFocuser> device(new BareFocuser());
