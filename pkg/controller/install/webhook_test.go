package install

import (
	"testing"

	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func rule(groups, resources []string) admissionregistrationv1.RuleWithOperations {
	return admissionregistrationv1.RuleWithOperations{
		Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
		Rule: admissionregistrationv1.Rule{
			APIGroups:   groups,
			APIVersions: []string{"v1"},
			Resources:   resources,
		},
	}
}

func TestValidWebhookRules(t *testing.T) {
	tests := []struct {
		name    string
		rules   []admissionregistrationv1.RuleWithOperations
		wantErr bool
		skip    string
	}{
		{
			name:    "WildcardGroupRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"*"}, []string{"pods"})},
			wantErr: true,
		},
		{
			name:    "OLMGroupRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"operators.coreos.com"}, []string{"clusterserviceversions"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationWildcardResourceRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"*"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationCamelCaseKindRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"MutatingWebhookConfiguration"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationMutatingResourceRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"mutatingwebhookconfigurations"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationValidatingResourceRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"validatingwebhookconfigurations"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationStatusSubresourceRejected",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"validatingwebhookconfigurations/status"})},
			wantErr: true,
		},
		{
			name:    "AdmissionRegistrationBenignResourceAccepted",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"admissionregistration.k8s.io"}, []string{"leases"})},
			wantErr: false,
		},
		{
			name:    "OtherGroupAccepted",
			rules:   []admissionregistrationv1.RuleWithOperations{rule([]string{"apps"}, []string{"deployments"})},
			wantErr: false,
		},
		{
			name:    "EmptyRulesAccepted",
			rules:   nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			err := ValidWebhookRules(tt.rules)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestListToMapAndContains(t *testing.T) {
	m := listToMap([]string{"a", "b", "a"})
	require.Len(t, m, 2)
	require.True(t, contains(m, "a"))
	require.True(t, contains(m, "b"))
	require.False(t, contains(m, "c"))
	require.Empty(t, listToMap(nil))
}
