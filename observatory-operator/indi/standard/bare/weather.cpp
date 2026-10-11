// The WEATHER mixin on a plain INDI::DefaultDevice. The mixin defines
// WEATHER_PARAMETERS and WEATHER_STATUS only when the driver adds a
// parameter and marks one critical, so the driver adds one of each:
// the temperature, which every weather driver in libindi reports.
#include <defaultdevice.h>
#include <indiweatherinterface.h>
#include <cstring>
#include <memory>

class BareWeather : public INDI::DefaultDevice, public INDI::WeatherInterface
{
  public:
    BareWeather() : INDI::WeatherInterface(this) {}
    const char *getDefaultName() override { return "Bare Weather"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        WI::initProperties(MAIN_CONTROL_TAB, "Parameters");
        addParameter("WEATHER_TEMPERATURE", "Temperature (C)", -10, 30, 15);
        setCriticalParameter("WEATHER_TEMPERATURE");
        setDriverInterface(WEATHER_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        WI::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && WI::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewSwitch(const char *dev, const char *name, ISState *states, char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && WI::processSwitch(dev, name, states, names, n))
            return true;
        return INDI::DefaultDevice::ISNewSwitch(dev, name, states, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    IPState updateWeather() override { return IPS_OK; }
};

static std::unique_ptr<BareWeather> device(new BareWeather());
