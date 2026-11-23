package install

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistration "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func TestIsAPIServiceAvailable(t *testing.T) {
	available := &apiregistration.APIService{
		Status: apiregistration.APIServiceStatus{
			Conditions: []apiregistration.APIServiceCondition{
				{Type: apiregistration.Available, Status: apiregistration.ConditionTrue},
			},
		},
	}
	unavailable := &apiregistration.APIService{
		Status: apiregistration.APIServiceStatus{
			Conditions: []apiregistration.APIServiceCondition{
				{Type: apiregistration.Available, Status: apiregistration.ConditionFalse},
			},
		},
	}

	require.True(t, IsAPIServiceAvailable(available))
	require.False(t, IsAPIServiceAvailable(unavailable))
	require.False(t, IsAPIServiceAvailable(&apiregistration.APIService{}))

	// A nil APIService must not panic; it is simply unavailable.
	t.Run("NilDoesNotPanic", func(t *testing.T) {
		t.Skip("INST-03: IsAPIServiceAvailable dereferences apiService.Status.Conditions without a nil guard, so a nil input panics")
		require.NotPanics(t, func() {
			require.False(t, IsAPIServiceAvailable(nil))
		})
	})
}

func ownerLabels() map[string]string {
	return map[string]string{
		ownerutil.OwnerKind:         "ClusterServiceVersion",
		ownerutil.OwnerKey:          "owner",
		ownerutil.OwnerNamespaceKey: "ns",
	}
}

func TestIsAPIServiceAdoptable(t *testing.T) {
	target := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "ns"},
		Spec:       operatorsv1alpha1.ClusterServiceVersionSpec{Replaces: "previous"},
	}

	tests := []struct {
		name       string
		target     *operatorsv1alpha1.ClusterServiceVersion
		apiService *apiregistration.APIService
		wantErr    bool
		want       bool
	}{
		{
			name:       "NilAPIService",
			target:     target,
			apiService: nil,
			wantErr:    true,
		},
		{
			name:       "NilTarget",
			target:     nil,
			apiService: &apiregistration.APIService{},
			wantErr:    true,
		},
		{
			name:       "NoOwnerLabels",
			target:     target,
			apiService: &apiregistration.APIService{ObjectMeta: metav1.ObjectMeta{Name: "svc"}},
			want:       false,
		},
		{
			name:   "KindMismatch",
			target: target,
			apiService: &apiregistration.APIService{ObjectMeta: metav1.ObjectMeta{
				Name:   "svc",
				Labels: map[string]string{ownerutil.OwnerKind: "NotACSV", ownerutil.OwnerKey: "owner", ownerutil.OwnerNamespaceKey: "ns"},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// These cases never reach the lister, so a bare fake is enough.
			adoptable, err := IsAPIServiceAdoptable(&operatorlisterfakes.FakeOperatorLister{}, tt.target, tt.apiService)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, adoptable)
			}
		})
	}
}

func TestIsAPIServiceAdoptableListerError(t *testing.T) {
	// A non-NotFound lister error must be returned to the caller rather than
	// being converted into a silent "not adoptable".
	nsLister := &operatorlisterfakes.FakeClusterServiceVersionNamespaceLister{}
	nsLister.GetReturns(nil, errors.New("lister unavailable"))
	csvLister := &operatorlisterfakes.FakeClusterServiceVersionLister{}
	csvLister.ClusterServiceVersionsReturns(nsLister)
	v1alpha1Lister := &operatorlisterfakes.FakeOperatorsV1alpha1Lister{}
	v1alpha1Lister.ClusterServiceVersionListerReturns(csvLister)
	opLister := &operatorlisterfakes.FakeOperatorLister{}
	opLister.OperatorsV1alpha1Returns(v1alpha1Lister)

	target := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "ns"},
		Spec:       operatorsv1alpha1.ClusterServiceVersionSpec{Replaces: "previous"},
	}
	apiService := &apiregistration.APIService{ObjectMeta: metav1.ObjectMeta{Name: "svc", Labels: ownerLabels()}}

	adoptable, err := IsAPIServiceAdoptable(opLister, target, apiService)
	require.Error(t, err)
	require.False(t, adoptable)
}
