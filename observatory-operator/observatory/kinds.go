package observatory

// Kind names one resource of the group: its kind and the plural that
// its URL uses.
type Kind struct {
	Name   string
	Plural string
}

// The kinds, from the top of the tree down: the site and its
// telescopes, the optics, the 14 device kinds, the guider, and the
// reservation. The 14 device kinds are the ones that have a simulator
// in the indi-simulators image.
var (
	ObservatoryKind  = Kind{"Observatory", "observatories"}
	TelescopeKind    = Kind{"Telescope", "telescopes"}
	OpticalTubeKind  = Kind{"OpticalTube", "opticaltubes"}
	OpticalTrainKind = Kind{"OpticalTrain", "opticaltrains"}

	MountKind           = Kind{"Mount", "mounts"}
	CameraKind          = Kind{"Camera", "cameras"}
	FilterWheelKind     = Kind{"FilterWheel", "filterwheels"}
	FocuserKind         = Kind{"Focuser", "focusers"}
	RotatorKind         = Kind{"Rotator", "rotators"}
	DustCapKind         = Kind{"DustCap", "dustcaps"}
	FlatPanelKind       = Kind{"FlatPanel", "flatpanels"}
	PolarAlignerKind    = Kind{"PolarAligner", "polaraligners"}
	GPSKind             = Kind{"GPS", "gpses"}
	DomeKind            = Kind{"Dome", "domes"}
	WeatherStationKind  = Kind{"WeatherStation", "weatherstations"}
	SkyQualityMeterKind = Kind{"SkyQualityMeter", "skyqualitymeters"}
	SwitchKind          = Kind{"Switch", "switches"}
	ReceiverKind        = Kind{"Receiver", "receivers"}

	GuiderKind      = Kind{"Guider", "guiders"}
	ReservationKind = Kind{"Reservation", "reservations"}
)

// DeviceKinds lists the 14 device kinds, in the order of plan 06's
// table. A reservation connects them in its own order, which plan 07
// gives.
var DeviceKinds = []Kind{
	MountKind, CameraKind, FilterWheelKind, FocuserKind, RotatorKind,
	DustCapKind, FlatPanelKind, PolarAlignerKind, GPSKind, DomeKind,
	WeatherStationKind, SkyQualityMeterKind, SwitchKind, ReceiverKind,
}

// Kinds lists all 20 kinds.
var Kinds = []Kind{
	ObservatoryKind, TelescopeKind, OpticalTubeKind, OpticalTrainKind,
	MountKind, CameraKind, FilterWheelKind, FocuserKind, RotatorKind,
	DustCapKind, FlatPanelKind, PolarAlignerKind, GPSKind, DomeKind,
	WeatherStationKind, SkyQualityMeterKind, SwitchKind, ReceiverKind,
	GuiderKind, ReservationKind,
}

// Path answers the URL of the kind's collection in one namespace, such
// as /apis/observatory.liken.sh/v1alpha1/namespaces/observatory/mounts.
func (k Kind) Path(namespace string) string {
	return "/apis/" + APIVersion + "/namespaces/" + namespace + "/" + k.Plural
}

// KindNamed answers the kind of a name, such as Mount, and false for a
// name that is no kind of the group.
func KindNamed(name string) (Kind, bool) {
	for _, kind := range Kinds {
		if kind.Name == name {
			return kind, true
		}
	}
	return Kind{}, false
}
