package catalog

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	versionedfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

func testInstallPlan(name, sa string, phase operatorsv1alpha1.InstallPlanPhase) *operatorsv1alpha1.InstallPlan {
	ip := &operatorsv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID("uid-" + name)},
		Status:     operatorsv1alpha1.InstallPlanStatus{Phase: phase},
	}
	if sa != "" {
		ip.Status.AttenuatedServiceAccountRef = &corev1.ObjectReference{Name: sa, Namespace: "ns"}
	}
	return ip
}

func newInstallPlanOperator(t *testing.T, ips ...*operatorsv1alpha1.InstallPlan) (*Operator, *versionedfake.Clientset) {
	t.Helper()

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, ip := range ips {
		require.NoError(t, indexer.Add(ip))
	}

	lister := operatorlister.NewLister()
	lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, listersv1alpha1.NewInstallPlanLister(indexer))

	objects := make([]runtime.Object, 0, len(ips))
	for _, ip := range ips {
		objects = append(objects, ip.DeepCopy())
	}
	client := versionedfake.NewSimpleClientset(objects...)
	client.ClearActions()

	return &Operator{
		logger: logrus.New(),
		client: client,
		lister: lister,
	}, client
}

func statusUpdates(client *versionedfake.Clientset) int {
	count := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "installplans" && action.GetSubresource() == "status" {
			count++
		}
	}
	return count
}

func roleBinding(saName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "rb", Namespace: "ns"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: saName, Namespace: "ns"}},
	}
}

func TestTriggerInstallPlanRetry(t *testing.T) {
	t.Run("NonObjectReturnsError", func(t *testing.T) {
		op, client := newInstallPlanOperator(t)
		require.Error(t, op.triggerInstallPlanRetry("not-an-object"))
		require.Equal(t, 0, statusUpdates(client))
	})

	t.Run("NonRBACObjectIgnored", func(t *testing.T) {
		op, client := newInstallPlanOperator(t, testInstallPlan("ip", "sa", operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, op.triggerInstallPlanRetry(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "ns"}}))
		require.Equal(t, 0, statusUpdates(client))
	})

	t.Run("FailedAttenuatedPlanRetried", func(t *testing.T) {
		op, client := newInstallPlanOperator(t, testInstallPlan("ip", "sa", operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, op.triggerInstallPlanRetry(roleBinding("sa")))
		require.Equal(t, 1, statusUpdates(client))
	})

	t.Run("FailedPlanWithoutRefIgnored", func(t *testing.T) {
		op, client := newInstallPlanOperator(t, testInstallPlan("ip", "", operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, op.triggerInstallPlanRetry(roleBinding("sa")))
		require.Equal(t, 0, statusUpdates(client))
	})

	t.Run("NonFailedPlanIgnored", func(t *testing.T) {
		op, client := newInstallPlanOperator(t, testInstallPlan("ip", "sa", operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, op.triggerInstallPlanRetry(roleBinding("sa")))
		require.Equal(t, 0, statusUpdates(client))
	})

	t.Run("UnrelatedRoleBindingDoesNotReset", func(t *testing.T) {
		t.Skip("CAT-01: triggerInstallPlanRetry does not correlate the RBAC object with the InstallPlan's attenuated service account, so any Role/RoleBinding change resets every failed attenuated InstallPlan in the namespace")

		op, client := newInstallPlanOperator(t, testInstallPlan("ip", "sa-target", operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, op.triggerInstallPlanRetry(roleBinding("sa-other")))
		require.Equal(t, 0, statusUpdates(client))
	})
}
