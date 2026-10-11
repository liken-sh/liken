// The ROTATOR mixin on a plain INDI::DefaultDevice. INDI::Rotator adds
// Presets and Goto, which the mixin does not define.
#include <defaultdevice.h>
#include <indirotatorinterface.h>
#include <cstring>
#include <memory>

class BareRotator : public INDI::DefaultDevice, public INDI::RotatorInterface
{
  public:
    BareRotator() : INDI::RotatorInterface(this)
    {
        RI::SetCapability(ROTATOR_CAN_ABORT | ROTATOR_CAN_HOME | ROTATOR_CAN_SYNC | ROTATOR_CAN_REVERSE |
                          ROTATOR_HAS_BACKLASH);
    }
    const char *getDefaultName() override { return "Bare Rotator"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        RI::initProperties(MAIN_CONTROL_TAB);
        setDriverInterface(ROTATOR_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        RI::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && RI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && RI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    IPState MoveRotator(double) override { return IPS_OK; }
};

static std::unique_ptr<BareRotator> device(new BareRotator());
