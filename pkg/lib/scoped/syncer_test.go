package scoped

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/reference"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
	clocktesting "k8s.io/utils/clock/testing"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	versionedfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

func syncerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1.AddToScheme(scheme))
	return scheme
}

func newSyncer(t *testing.T, scheme *runtime.Scheme, sa *corev1.ServiceAccount, og *v1.OperatorGroup, saErr error) (*UserDefinedServiceAccountSyncer, *versionedfake.Clientset) {
	t.Helper()

	k8sClient := k8sfake.NewSimpleClientset()
	if saErr != nil {
		k8sClient.PrependReactor("get", "serviceaccounts", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, saErr
		})
	} else if sa != nil {
		_, err := k8sClient.CoreV1().ServiceAccounts(sa.Namespace).Create(context.TODO(), sa, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	// The syncer applies the olm.managed label to the SA; make the fake patch a no-op.
	k8sClient.PrependReactor("patch", "serviceaccounts", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, sa, nil
	})

	opClient := operatorclient.NewClient(k8sClient, apiextensionsfake.NewSimpleClientset(), apiregistrationfake.NewSimpleClientset())

	versionedClient := versionedfake.NewSimpleClientset()
	if og != nil {
		_, err := versionedClient.OperatorsV1().OperatorGroups(og.Namespace).Create(context.TODO(), og, metav1.CreateOptions{})
		require.NoError(t, err)
		// Reset actions so only calls made by the syncer are counted.
		versionedClient.ClearActions()
	}

	logger, _ := test.NewNullLogger()
	s := NewUserDefinedServiceAccountSyncer(logger, scheme, opClient, versionedClient)
	s.clock = clocktesting.NewFakeClock(time.Unix(1700000000, 0))
	return s, versionedClient
}

func statusUpdateCount(client *versionedfake.Clientset) int {
	count := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "operatorgroups" && action.GetSubresource() == "status" {
			count++
		}
	}
	return count
}

func operatorGroup(saName string, ref *corev1.ObjectReference) *v1.OperatorGroup {
	return &v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "og", Namespace: "ns"},
		Spec:       v1.OperatorGroupSpec{ServiceAccountName: saName},
		Status:     v1.OperatorGroupStatus{ServiceAccountRef: ref},
	}
}

func TestSyncOperatorGroup(t *testing.T) {
	t.Run("EmptySpecNoRefNoUpdate", func(t *testing.T) {
		scheme := syncerScheme(t)
		og := operatorGroup("", nil)
		s, client := newSyncer(t, scheme, nil, og, nil)

		out, err := s.SyncOperatorGroup(og.DeepCopy())
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, 0, statusUpdateCount(client))
	})

	t.Run("EmptySpecResetsRef", func(t *testing.T) {
		scheme := syncerScheme(t)
		og := operatorGroup("", &corev1.ObjectReference{Name: "sa", Namespace: "ns"})
		s, client := newSyncer(t, scheme, nil, og, nil)

		_, err := s.SyncOperatorGroup(og.DeepCopy())
		require.NoError(t, err)
		require.Equal(t, 1, statusUpdateCount(client))
	})

	t.Run("ServiceAccountNotFound", func(t *testing.T) {
		scheme := syncerScheme(t)
		og := operatorGroup("sa", nil)
		s, client := newSyncer(t, scheme, nil, og, apierrors.NewNotFound(schema.GroupResource{Resource: "serviceaccounts"}, "sa"))

		out, err := s.SyncOperatorGroup(og.DeepCopy())
		require.Error(t, err)
		require.NotNil(t, out)
		require.Equal(t, 1, statusUpdateCount(client))
	})

	t.Run("ExistingMatchingRefNoUpdate", func(t *testing.T) {
		scheme := syncerScheme(t)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "ns", UID: "uid-1", ResourceVersion: "1"},
		}
		ref, err := reference.GetReference(scheme, sa)
		require.NoError(t, err)

		og := operatorGroup("sa", ref)
		s, client := newSyncer(t, scheme, sa, og, nil)

		_, err = s.SyncOperatorGroup(og.DeepCopy())
		require.NoError(t, err)
		require.Equal(t, 0, statusUpdateCount(client))
	})

	t.Run("EquivalentRefDifferentResourceVersionDoesNotUpdate", func(t *testing.T) {
		scheme := syncerScheme(t)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "ns", UID: "uid-1", ResourceVersion: "2"},
		}
		storedRef := &corev1.ObjectReference{
			Kind: "ServiceAccount", APIVersion: "v1", Name: "sa", Namespace: "ns", UID: "uid-1", ResourceVersion: "1",
		}
		og := operatorGroup("sa", storedRef)
		s, client := newSyncer(t, scheme, sa, og, nil)

		_, err := s.SyncOperatorGroup(og.DeepCopy())
		require.NoError(t, err)
		require.Equal(t, 0, statusUpdateCount(client))
	})

}
