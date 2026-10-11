// The FILTER mixin on a plain INDI::DefaultDevice. A camera with a
// built-in wheel hosts the same mixin, so its standard set is the
// mixin's, not INDI::FilterWheel's.
#include <defaultdevice.h>
#include <indifilterinterface.h>
#include <cstring>
#include <memory>

class BareFilter : public INDI::DefaultDevice, public INDI::FilterInterface
{
  public:
    BareFilter() : INDI::FilterInterface(this) {}
    const char *getDefaultName() override { return "Bare Filter"; }
    bool initProperties() override
    {
        INDI::DefaultDevice::initProperties();
        INDI::FilterInterface::initProperties(FILTER_TAB);
        setDriverInterface(FILTER_INTERFACE);
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        INDI::DefaultDevice::updateProperties();
        INDI::FilterInterface::updateProperties();
        return true;
    }
    bool ISNewNumber(const char *dev, const char *name, double values[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::FilterInterface::processNumber(dev, name, values, names, n))
            return true;
        return INDI::DefaultDevice::ISNewNumber(dev, name, values, names, n);
    }
    bool ISNewText(const char *dev, const char *name, char *texts[], char *names[], int n) override
    {
        if (dev && !strcmp(dev, getDeviceName()) && INDI::FilterInterface::processText(dev, name, texts, names, n))
            return true;
        return INDI::DefaultDevice::ISNewText(dev, name, texts, names, n);
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
    int QueryFilter() override { return CurrentFilter; }
    bool SelectFilter(int position) override
    {
        CurrentFilter = position;
        SelectFilterDone(position);
        return true;
    }
};

static std::unique_ptr<BareFilter> device(new BareFilter());
