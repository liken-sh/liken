package observatory

// Kind is a resource kind of the API group, with the plural name that
// its URLs use.
type Kind struct {
	Name   string
	Plural string
}

// The kinds of the group. Each device kind serves one or more INDI
// interfaces, which Standards lists.
var (
	ObservatoryKind  = Kind{"Observatory", "observatories"}
	TelescopeKind    = Kind{"Telescope", "telescopes"}
	OpticalTubeKind  = Kind{"OpticalTube", "opticaltubes"}
	OpticalTrainKind = Kind{"OpticalTrain", "opticaltrains"}

	MountKind          = Kind{"Mount", "mounts"}
	CameraKind         = Kind{"Camera", "cameras"}
	FilterWheelKind    = Kind{"FilterWheel", "filterwheels"}
	FocuserKind        = Kind{"Focuser", "focusers"}
	RotatorKind        = Kind{"Rotator", "rotators"}
	DustCapKind        = Kind{"DustCap", "dustcaps"}
	FlatPanelKind      = Kind{"FlatPanel", "flatpanels"}
	PolarAlignerKind   = Kind{"PolarAligner", "polaraligners"}
	GPSKind            = Kind{"GPS", "gpses"}
	DomeKind           = Kind{"Dome", "domes"}
	WeatherStationKind = Kind{"WeatherStation", "weatherstations"}
	SwitchKind         = Kind{"Switch", "switches"}
	ReceiverKind       = Kind{"Receiver", "receivers"}

	GuiderKind      = Kind{"Guider", "guiders"}
	ReservationKind = Kind{"Reservation", "reservations"}
	HorizonKind     = Kind{"Horizon", "horizons"}
)

var DeviceKinds = []Kind{
	MountKind, CameraKind, FilterWheelKind, FocuserKind, RotatorKind,
	DustCapKind, FlatPanelKind, PolarAlignerKind, GPSKind, DomeKind,
	WeatherStationKind, SwitchKind, ReceiverKind,
}

var Kinds = []Kind{
	ObservatoryKind, TelescopeKind, OpticalTubeKind, OpticalTrainKind,
	MountKind, CameraKind, FilterWheelKind, FocuserKind, RotatorKind,
	DustCapKind, FlatPanelKind, PolarAlignerKind, GPSKind, DomeKind,
	WeatherStationKind, SwitchKind, ReceiverKind,
	GuiderKind, ReservationKind, HorizonKind,
}

// HasStatus reports whether a kind has a status subresource. A Horizon
// has none, because the operator writes nothing about it. A Telescope's
// HorizonFound condition reports whether its Horizon exists.
func (k Kind) HasStatus() bool { return k != HorizonKind }

// Path returns the URL of the kind's collection in a namespace, such
// as /apis/observatory.liken.sh/v1alpha1/namespaces/observatory/mounts.
func (k Kind) Path(namespace string) string {
	return "/apis/" + APIVersion + "/namespaces/" + namespace + "/" + k.Plural
}

// KindNamed returns the kind with the given name, such as Mount, or
// false when the group has no such kind.
func KindNamed(name string) (Kind, bool) {
	for _, kind := range Kinds {
		if kind.Name == name {
			return kind, true
		}
	}
	return Kind{}, false
}
