// The GPS mixin on a plain INDI::DefaultDevice. The driver reports a
// fix with no hardware, so the mixin's properties take the state Ok.
#include <defaultdevice.h>
#include <indigpsinterface.h>
#include <cstring>
#include <memory>

class BareGPS : public INDI::DefaultDevice, public INDI::GPSInterface
{
  public:
    BareGPS() : INDI::GPSInterface(this) {}
    const char *getDefaultName() override { return "Bare GPS"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        GPSI::initProperties(MAIN_CONTROL_TAB);
        setDriverInterface(GPS_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        GPSI::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && GPSI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && GPSI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    IPState updateGPS() override { return IPS_OK; }
};

static std::unique_ptr<BareGPS> device(new BareGPS());
