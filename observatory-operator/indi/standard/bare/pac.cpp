// The PAC mixin, a polar alignment corrector, on a plain
// INDI::DefaultDevice. The mixin has no base class.
#include <defaultdevice.h>
#include <indipacinterface.h>
#include <cstring>
#include <memory>

class BarePAC : public INDI::DefaultDevice, public INDI::PACInterface
{
  public:
    BarePAC() : INDI::PACInterface(this)
    {
        PACI::SetCapability(PAC_HAS_SPEED | PAC_CAN_REVERSE | PAC_HAS_POSITION | PAC_CAN_HOME |
                            PAC_HAS_BACKLASH | PAC_CAN_SYNC);
    }
    const char *getDefaultName() override { return "Bare PAC"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        PACI::initProperties(MAIN_CONTROL_TAB);
        setDriverInterface(AUX_INTERFACE | PAC_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        PACI::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && PACI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && PACI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BarePAC> device(new BarePAC());
