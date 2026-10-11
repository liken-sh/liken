// INDI::CCD with every capability. CCD has no mixin, so the driver
// inherits the base class.
#include <indiccd.h>
#include <memory>

class BareCCD : public INDI::CCD
{
  public:
    const char *getDefaultName() override { return "Bare CCD"; }
    bool initProperties() override
    {
        // The capabilities go after CCD::initProperties. With
        // CCD_HAS_STREAMING, SetCCDCapability builds the stream manager,
        // which copies the device name and the driver interface once.
        // Both are empty before CCD::initProperties runs: the streaming
        // properties then name no device, and CCD_STREAM_FRAME has no
        // name.
        INDI::CCD::initProperties();
        SetCCDCapability(CCD_CAN_BIN | CCD_CAN_SUBFRAME | CCD_CAN_ABORT | CCD_HAS_GUIDE_HEAD |
                         CCD_HAS_ST4_PORT | CCD_HAS_SHUTTER | CCD_HAS_COOLER | CCD_HAS_BAYER |
                         CCD_HAS_STREAMING | CCD_HAS_DSP);
        // CCD_CAPTURE_FORMAT has one element for each format that the
        // driver adds, and a driver adds at least one.
        addCaptureFormat({"INDI_MONO", "Mono", 16, true});
        addAuxControls();
        return true;
    }
    bool updateProperties() override
    {
        if (isConnected())
        {
            SetCCDParams(1280, 1024, 16, 5.2, 5.2);
            SetGuiderParams(640, 480, 16, 5.2, 5.2);
        }
        return INDI::CCD::updateProperties();
    }
    bool Connect() override { return true; }
    bool Disconnect() override { return true; }
};

static std::unique_ptr<BareCCD> device(new BareCCD());
