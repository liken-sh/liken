package electiontest_test

// The fake answers the Lease requests the way the API server does, so
// a test that passes against it passes against the real one. These
// tests hold it to that through client-go's typed client, the client
// the election uses.

import (
	"net/http"
	"testing"
	"time"

	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"

	"github.com/liken-sh/liken/kubernetes/election/electiontest"
)

func leases(t *testing.T, server *electiontest.Server) coordinationv1.LeaseInterface {
	t.Helper()
	client, err := coordinationv1.NewForConfig(server.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	return client.Leases("liken-system")
}

func lease(holder string) *coordination.Lease {
	return &coordination.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "an-operator", Namespace: "liken-system"},
		Spec:       coordination.LeaseSpec{HolderIdentity: &holder},
	}
}

func TestACreateOfALeaseThatExistsConflicts(t *testing.T) {
	server := electiontest.New("an-operator", "liken-system")
	client := leases(t, server)
	if _, err := client.Create(t.Context(), lease("a"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err := client.Create(t.Context(), lease("b"), metav1.CreateOptions{})

	if !apierrors.IsAlreadyExists(err) || server.Holder() != "a" {
		t.Errorf("second create = %v, holder %q; want AlreadyExists and the first holder", err, server.Holder())
	}
}

func TestAnUpdateFromAStaleVersionConflicts(t *testing.T) {
	server := electiontest.New("an-operator", "liken-system")
	client := leases(t, server)
	server.HoldAs("a")
	read, err := client.Get(t.Context(), "an-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server.HoldAs("b")

	_, err = client.Update(t.Context(), read, metav1.UpdateOptions{})

	if !apierrors.IsConflict(err) || server.Holder() != "b" {
		t.Errorf("update = %v, holder %q; want a conflict and the newer holder", err, server.Holder())
	}
}

func TestAWriteLandsAfterTheNextRead(t *testing.T) {
	server := electiontest.New("an-operator", "liken-system")
	client := leases(t, server)
	server.HoldAs("a")
	server.LandAWriteAfterTheNextRead()
	read, err := client.Get(t.Context(), "an-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Update(t.Context(), read, metav1.UpdateOptions{})

	if !apierrors.IsConflict(err) {
		t.Errorf("update after the landed write = %v, want a conflict", err)
	}
}

func TestAMissingLeaseIsNotFound(t *testing.T) {
	server := electiontest.New("an-operator", "liken-system")
	client := leases(t, server)

	_, read := client.Get(t.Context(), "an-operator", metav1.GetOptions{})
	_, update := client.Update(t.Context(), lease("a"), metav1.UpdateOptions{})
	_, other := client.Get(t.Context(), "another-operator", metav1.GetOptions{})

	for name, err := range map[string]error{"read": read, "update": update, "read of another name": other} {
		if !apierrors.IsNotFound(err) {
			t.Errorf("%s = %v, want NotFound", name, err)
		}
	}
	if server.Holder() != "" {
		t.Errorf("holder = %q, want none", server.Holder())
	}
}

func TestARefusalAnswersItsReasonUntilItIsAllowed(t *testing.T) {
	cases := []struct {
		status int
		is     func(error) bool
	}{
		{http.StatusForbidden, apierrors.IsForbidden},
		{http.StatusNotFound, apierrors.IsNotFound},
		{http.StatusInternalServerError, apierrors.IsInternalError},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			server := electiontest.New("an-operator", "liken-system")
			client := leases(t, server)
			server.Refuse(http.MethodPost, c.status)

			_, refused := client.Create(t.Context(), lease("a"), metav1.CreateOptions{})
			server.Allow(http.MethodPost)
			_, allowed := client.Create(t.Context(), lease("a"), metav1.CreateOptions{})

			if !c.is(refused) || allowed != nil || server.Count(http.MethodPost) != 2 {
				t.Errorf("refused = %v, allowed = %v, creates = %d", refused, allowed, server.Count(http.MethodPost))
			}
		})
	}
}

func TestAHeldLeaseNamesItsRenewal(t *testing.T) {
	server := electiontest.New("an-operator", "liken-system")
	client := leases(t, server)
	renewed := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	server.HoldAsRenewedAt("a", renewed)

	read, err := client.Get(t.Context(), "an-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Spec.RenewTime.Time.Equal(renewed) || *read.Spec.LeaseDurationSeconds != 30 {
		t.Errorf("renewed %v for %ds, want %v for 30s", read.Spec.RenewTime, *read.Spec.LeaseDurationSeconds, renewed)
	}
}
