package labeller

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	rbacv1applyconfigurations "k8s.io/client-go/applyconfigurations/rbac/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func ownedLabels() map[string]string {
	return map[string]string{
		ownerutil.OwnerKey:          "owner",
		ownerutil.OwnerNamespaceKey: "ns",
		ownerutil.OwnerKind:         "ClusterServiceVersion",
	}
}

func roleWithLabels(name string, extra map[string]string) *rbacv1.Role {
	merged := ownedLabels()
	for k, v := range extra {
		merged[k] = v
	}
	return &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: merged}}
}

func TestContentHashLabeler(t *testing.T) {
	tests := []struct {
		name        string
		invoked     interface{}
		existing    []*rbacv1.Role
		listErr     error
		doneReturns bool
		hasherErr   error
		wantErr     bool
		wantExit    bool
		wantDone    int
		wantApply   bool
	}{
		{
			name:    "ManagedButUnhashedItemBlocksExit",
			invoked: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing: []*rbacv1.Role{
				roleWithLabels("needs-hash", map[string]string{
					install.OLMManagedLabelKey: install.OLMManagedLabelValue,
				}),
			},
			doneReturns: true,
			// An object carrying olm.managed but not the content-hash label is
			// still incomplete, so done() must not be called and we must not exit.
			wantExit: false,
			wantDone: 0,
		},
		{
			name:        "AllItemsHashedExits",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    []*rbacv1.Role{roleWithLabels("done", map[string]string{resolver.ContentHashLabelKey: "hash"})},
			doneReturns: true,
			wantExit:    true,
			wantDone:    1,
		},
		{
			// Negative control: an owned object lacking BOTH labels already blocks
			// the exit today. This isolates LAB-01 to the managed-but-unhashed case.
			name:        "OwnedUnhashedUnmanagedBlocksExit",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    []*rbacv1.Role{roleWithLabels("needs-hash", nil)},
			doneReturns: true,
			wantExit:    false,
			wantDone:    0,
		},
		{
			// One complete object must not mask one incomplete object.
			name:    "MixedHashedAndUnhashedBlocksExit",
			invoked: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing: []*rbacv1.Role{
				roleWithLabels("hashed", map[string]string{resolver.ContentHashLabelKey: "hash"}),
				roleWithLabels("needs-hash", nil),
			},
			doneReturns: true,
			wantExit:    false,
			wantDone:    0,
		},
		{
			name:        "OwnedManagedAndHashedExits",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    []*rbacv1.Role{roleWithLabels("done", map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, resolver.ContentHashLabelKey: "hash"})},
			doneReturns: true,
			wantExit:    true,
			wantDone:    1,
		},
		{
			// Objects this labeller does not own never block completion.
			name:        "AllUnownedExits",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    []*rbacv1.Role{{ObjectMeta: metav1.ObjectMeta{Name: "unowned", Namespace: "ns"}}},
			doneReturns: true,
			wantExit:    true,
			wantDone:    1,
		},
		{
			name:        "EmptyListExits",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    nil,
			doneReturns: true,
			wantExit:    true,
			wantDone:    1,
		},
		{
			name:        "NotAllLabellersDoneDoesNotExit",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			existing:    []*rbacv1.Role{roleWithLabels("done", map[string]string{resolver.ContentHashLabelKey: "hash"})},
			doneReturns: false,
			wantExit:    false,
			wantDone:    1,
		},
		{
			name:        "ListErrorDoesNotExitOrCallDone",
			invoked:     &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "trigger", Namespace: "ns"}},
			listErr:     errors.New("boom"),
			doneReturns: true,
			wantExit:    false,
			wantDone:    0,
		},
		{
			name:      "EligibleObjectAppliesHash",
			invoked:   roleWithLabels("needs-hash", nil),
			wantApply: true,
		},
		{
			name:      "WrongTypeReturnsError",
			invoked:   &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "wrong", Namespace: "ns"}},
			wantErr:   true,
			wantApply: false,
		},
		{
			name:      "HasherErrorReturnsError",
			invoked:   roleWithLabels("needs-hash", nil),
			hasherErr: errors.New("cannot hash"),
			wantErr:   true,
			wantApply: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var exitCalls, doneCalls, applyCalls int
			appliedLabels := map[string]string{}

			oldExit := exit
			exit = func(int) { exitCalls++ }
			t.Cleanup(func() { exit = oldExit })

			logger, _ := test.NewNullLogger()

			handler := ContentHashLabeler[*rbacv1.Role, *rbacv1applyconfigurations.RoleApplyConfiguration](
				context.Background(),
				logger,
				ContentHashFilter,
				func(*rbacv1.Role) (string, error) { return "hash", tt.hasherErr },
				func(labels.Selector) ([]*rbacv1.Role, error) { return tt.existing, tt.listErr },
				rbacv1applyconfigurations.Role,
				func(namespace string, _ context.Context, cfg *rbacv1applyconfigurations.RoleApplyConfiguration, _ metav1.ApplyOptions) (*rbacv1.Role, error) {
					applyCalls++
					for k, v := range cfg.Labels {
						appliedLabels[k] = v
					}
					return &rbacv1.Role{}, nil
				},
			)

			err := handler(func() bool { doneCalls++; return tt.doneReturns })(tt.invoked)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantExit, exitCalls == 1, "exit calls")
			require.Equal(t, tt.wantDone, doneCalls, "done calls")
			require.Equal(t, tt.wantApply, applyCalls == 1, "apply calls")

			if tt.wantApply {
				require.Equal(t, "hash", appliedLabels[resolver.ContentHashLabelKey])
			}
		})
	}
}

func TestHasHashLabel(t *testing.T) {
	require.False(t, hasHashLabel(&rbacv1.Role{}))
	require.True(t, hasHashLabel(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{resolver.ContentHashLabelKey: "h"}}}))
}
