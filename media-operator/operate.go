package main

// The operator's loop has the shape liken's own operators use:
// level-triggered, woken by a watch, with a ticker as the backstop,
// and a reconcile before the first event ever arrives.
//
// A pass reads the whole collection instead of acting on the object
// an event carried. The event is only a wake. Every pass derives
// every status from what the API server holds right now, so a lost
// event costs at most one backstop tick, a reordered burst collapses
// into one pass, and a restarted operator starts correct with no
// replay.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// The operator's own environment: the player image it stamps into
// every playback pod, and the address it hears reports on. The
// Deployment must set PLAYER_IMAGE and MEDIA_OPERATOR_URL, because
// neither is discoverable from inside the pod: the image pair is a
// release decision, and a pod cannot read the name of the Service in
// front of it. REPORT_ADDRESS has a default, because a container's
// own listen address is nobody's policy.
const (
	playerImageVariable   = "PLAYER_IMAGE"
	reportAddressVariable = "REPORT_ADDRESS"
)

const defaultReportAddress = ":8080"

// backstopInterval is how often the loop reconciles with nothing to
// prompt it. The tick is what recovers a lost watch event, a pod
// that changed phase, and a Player that appeared after its Play.
const backstopInterval = 10 * time.Second

// tokenBytes is sixteen random bytes, printed as thirty-two
// hexadecimal characters. The token proves a report comes from the
// pod the operator created for that Play, and it grants nothing
// else: a stolen token can misreport one play's position, never
// touch the API.
const tokenBytes = 16

// mintToken is a package variable so a test can mint a token it can
// predict.
var mintToken = func() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// operator holds what every pass needs. The report desk is a field
// rather than a global so a test builds an operator around a desk it
// can inspect.
type operator struct {
	client      *Client
	image       string
	operatorURL string
	reports     *reports
}

func operate() {
	// Setup failures end the process on purpose. The kubelet
	// restarts the pod with backoff, and the failure shows in
	// kubectl instead of hiding in a retry loop.
	image := os.Getenv(playerImageVariable)
	if image == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the player image\n", playerImageVariable)
		os.Exit(1)
	}
	operatorURL := os.Getenv(operatorURLVariable)
	if operatorURL == "" {
		fmt.Fprintf(os.Stderr, "%s is unset; the Deployment must name the Service a playback pod reports to\n", operatorURLVariable)
		os.Exit(1)
	}
	address := os.Getenv(reportAddressVariable)
	if address == "" {
		address = defaultReportAddress
	}

	client, err := InClusterClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
		os.Exit(1)
	}

	// One wake channel serves the watch and the report desk both,
	// because a wake carries no information beyond "read the
	// collection again".
	wake := make(chan struct{}, 1)
	desk := newReports(wake)
	media := &operator{client: client, image: image, operatorURL: operatorURL, reports: desk}

	go func() {
		// An endpoint that cannot listen leaves every Play's
		// position frozen, so the process ends instead of running an
		// operator that hears nothing.
		if err := http.ListenAndServe(address, reportHandler(desk)); err != nil {
			fmt.Fprintf(os.Stderr, "serving reports on %s: %v\n", address, err)
			os.Exit(1)
		}
	}()

	// The first list does two jobs: it proves the operator can read
	// plays at all, and its resourceVersion is where the watch
	// starts.
	list, err := ListPlays(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing plays: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("media.liken.sh: operating %d plays, reports on %s\n", len(list.Items), address)
	go watchPlays(client, list.Metadata.ResourceVersion, wake)

	ticker := time.NewTicker(backstopInterval)
	for {
		media.pass()
		select {
		case <-wake:
		case <-ticker.C:
		}
	}
}

// pass runs one reconcile over every Play in the cluster. A failure
// on one Play is reported and the pass continues, because one
// namespace's broken run must not freeze every other room's status.
func (o *operator) pass() {
	list, err := ListPlays(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing plays: %v\n", err)
		return
	}
	live := make(map[string]bool, len(list.Items))
	for index := range list.Items {
		play := &list.Items[index]
		live[runKey(play.Metadata.Namespace, play.Metadata.Name)] = true
		// A Play in a terminal phase is done. Its pod and claims
		// stay until the Play is deleted, and the garbage collector
		// takes them then, through the ownerReferences they carry.
		if terminalPhase(play.Status.Phase) {
			continue
		}
		if err := o.reconcile(play); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling play %s/%s: %v\n",
				play.Metadata.Namespace, play.Metadata.Name, err)
		}
	}
	o.reports.retain(live)
}

// reconcile takes one Play from the Player it names to the status it
// earns. The order matters: nothing is created until the Player is
// read and every URI resolves, so a Play that can never run leaves
// no half-built objects behind.
func (o *operator) reconcile(play *Play) error {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if playerName(play) == "" {
		return writePlayStatus(o.client, play, PlayStatus{
			Phase:   phaseFailed,
			Message: "the Play names no Player",
		})
	}

	player, err := GetPlayer(o.client, namespace, playerName(play))
	if errors.Is(err, ErrNotFound) {
		return writePlayStatus(o.client, play, derivePlayStatus(play, nil, nil, nil, nil))
	}
	if err != nil {
		return err
	}

	resolved, resolveErr := resolveURIs(play.Spec.URIs)
	if resolveErr != nil {
		return writePlayStatus(o.client, play, derivePlayStatus(play, player, resolveErr, nil, nil))
	}

	claim := buildClaim(play, player)
	if err := ensureClaim(o.client, claim); err != nil {
		return err
	}
	pod, err := o.ensurePod(play, claim, resolved)
	if err != nil {
		return err
	}
	return writePlayStatus(o.client, play,
		derivePlayStatus(play, player, nil, pod, o.reports.latestFor(namespace, name)))
}

// ensureClaim creates the claim once and never updates it. A Play's
// spec is immutable, and the Player it names is read at the start of
// the run, so the claim a run starts with is the claim it keeps: a
// Player edited mid-run changes the next Play, not this one.
//
// A 409 on the create is success, because it means another pass, or
// another copy of this operator, created the same claim first.
func ensureClaim(c *Client, claim *ResourceClaim) error {
	_, err := GetResourceClaim(c, claim.Metadata.Namespace, claim.Metadata.Name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, err := CreateResourceClaim(c, claim); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	return nil
}

// ensurePod creates the pod once per Play and never rebuilds it.
// There are two ways to hold a pod: this pass created it, in which
// case the freshly minted token goes to the desk, or a previous
// operator left it running, in which case the token is adopted from
// the pod's own environment, so the pod's reports stay accepted
// across an operator restart.
func (o *operator) ensurePod(play *Play, claim *ResourceClaim, resolved resolution) (*Pod, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	pod, err := GetPod(o.client, namespace, podName(name))
	if err == nil {
		o.adoptToken(play, pod)
		return pod, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	created, err := CreatePod(o.client, buildPod(play, claim, resolved, o.image, token, o.operatorURL))
	if errors.Is(err, ErrConflict) {
		// The pod already existed, so the token this pass minted
		// never reached it. The pod's own environment carries the
		// token its container holds, so that is the one to keep.
		created, err = GetPod(o.client, namespace, podName(name))
		if err != nil {
			return nil, err
		}
		o.adoptToken(play, created)
		return created, nil
	}
	if err != nil {
		return nil, err
	}
	o.reports.remember(namespace, name, token)
	return created, nil
}

// adoptToken reads the token out of a running pod's environment,
// which is where a minted token survives an operator restart. A pod
// with no token in it leaves the desk as it was.
func (o *operator) adoptToken(play *Play, pod *Pod) {
	if token := tokenFromPod(pod); token != "" {
		o.reports.remember(play.Metadata.Namespace, play.Metadata.Name, token)
	}
}
