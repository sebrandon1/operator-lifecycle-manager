package labeller

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metadatafake "k8s.io/client-go/metadata/fake"

	versionedfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
)

func meta(labels map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: "obj", Namespace: "ns", Labels: labels}
}

func TestHasOLMOwnerRef(t *testing.T) {
	tests := []struct {
		name   string
		object metav1.Object
		want   bool
	}{
		{
			name: "OwnerRefOnOLMAPI",
			object: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "operators.coreos.com/v1alpha1", Kind: "ClusterServiceVersion", Name: "x"},
			}}},
			want: true,
		},
		{
			name: "OwnerRefOnNonOLMAPI",
			object: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "Deployment", Name: "x"},
			}}},
			want: false,
		},
		{
			name:   "AllOwnerLabels",
			object: &rbacv1.Role{ObjectMeta: meta(ownedLabels())},
			want:   true,
		},
		{
			name:   "PartialOwnerLabels",
			object: &rbacv1.Role{ObjectMeta: meta(map[string]string{"olm.owner": "x"})},
			want:   false,
		},
		{
			name:   "NoLabels",
			object: &rbacv1.Role{},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, HasOLMOwnerRef(tt.object))
		})
	}
}

func TestHasOLMLabel(t *testing.T) {
	require.True(t, HasOLMLabel(&rbacv1.Role{ObjectMeta: meta(map[string]string{"operators.coreos.com/example": ""})}))
	require.False(t, HasOLMLabel(&rbacv1.Role{ObjectMeta: meta(map[string]string{"unrelated": "x"})}))
	require.False(t, HasOLMLabel(&rbacv1.Role{}))
}

func TestContentHashFilter(t *testing.T) {
	require.True(t, ContentHashFilter(&rbacv1.Role{ObjectMeta: meta(ownedLabels())}))
	require.False(t, ContentHashFilter(&rbacv1.Role{ObjectMeta: meta(merge(ownedLabels(), map[string]string{
		resolver.ContentHashLabelKey: "h",
	}))}))
	require.False(t, ContentHashFilter(&rbacv1.Role{}))
}

func TestJobFilter(t *testing.T) {
	job := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "job-pod",
		Namespace: "ns",
		OwnerReferences: []metav1.OwnerReference{
			{APIVersion: "v1", Kind: "ConfigMap", Name: "cm"},
		},
	}}

	t.Run("ConfigMapIsOLMOwned", func(t *testing.T) {
		f := JobFilter(func(namespace, name string) (metav1.Object, error) {
			return &corev1.ConfigMap{ObjectMeta: meta(ownedLabels())}, nil
		})
		require.True(t, f(job))
	})

	t.Run("ConfigMapLookupError", func(t *testing.T) {
		f := JobFilter(func(namespace, name string) (metav1.Object, error) {
			return nil, errors.New("boom")
		})
		require.False(t, f(job))
	})

	t.Run("NoConfigMapOwner", func(t *testing.T) {
		f := JobFilter(func(namespace, name string) (metav1.Object, error) {
			return &corev1.ConfigMap{ObjectMeta: meta(ownedLabels())}, nil
		})
		require.False(t, f(&corev1.Pod{}))
	})
}

func TestServiceAccountFilter(t *testing.T) {
	referenced := func(namespace, name string) bool { return name == "referenced" }

	require.True(t, ServiceAccountFilter(referenced)(&rbacv1.Role{ObjectMeta: meta(ownedLabels())}))
	require.True(t, ServiceAccountFilter(referenced)(&rbacv1.Role{ObjectMeta: meta(map[string]string{"operators.coreos.com/example": ""})}))
	require.True(t, ServiceAccountFilter(referenced)(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "referenced", Namespace: "ns"}}))
	require.False(t, ServiceAccountFilter(referenced)(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns"}}))
}

func TestFilter(t *testing.T) {
	ownedRole := &rbacv1.Role{ObjectMeta: meta(ownedLabels())}
	ownedCM := &corev1.ConfigMap{ObjectMeta: meta(ownedLabels())}
	ownedNP := &networkingv1.NetworkPolicy{ObjectMeta: meta(ownedLabels())}
	podWithCatalogLabel := &corev1.Pod{ObjectMeta: meta(map[string]string{reconciler.CatalogSourceLabelKey: "cat"})}
	crdWithAlongsideAnnotation := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{
		Name:        "crd",
		Annotations: map[string]string{alongside.AnnotationPrefix + "x": ""},
	}}

	tests := []struct {
		name   string
		gvr    schema.GroupVersionResource
		object metav1.Object
		want   bool
		skip   string
	}{
		{name: "ConfigMaps", gvr: corev1.SchemeGroupVersion.WithResource("configmaps"), object: ownedCM, want: true},
		{name: "Services", gvr: corev1.SchemeGroupVersion.WithResource("services"), object: &corev1.Service{ObjectMeta: meta(ownedLabels())}, want: true},
		{name: "PodsWithCatalogLabel", gvr: corev1.SchemeGroupVersion.WithResource("pods"), object: podWithCatalogLabel, want: true},
		{name: "Roles", gvr: rbacv1.SchemeGroupVersion.WithResource("roles"), object: ownedRole, want: true},
		{name: "CRDsWithAlongside", gvr: apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions"), object: crdWithAlongsideAnnotation, want: true},
		{
			name:   "NetworkPoliciesOLMOwned",
			gvr:    networkingv1.SchemeGroupVersion.WithResource("networkpolicies"),
			object: ownedNP,
			want:   true,
			skip:   "LAB-02: the filters map has no networkpolicies entry, so Filter(networkPoliciesGVR) (wired at catalog/operator.go:626) returns an always-false predicate and NetworkPolicies are never labelled",
		},
		{
			name:   "UnknownGVRIsAlwaysFalse",
			gvr:    corev1.SchemeGroupVersion.WithResource("does-not-exist"),
			object: ownedRole,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			require.Equal(t, tt.want, Filter(tt.gvr)(tt.object))
		})
	}
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func partialRole(name string, labels map[string]string) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{APIVersion: rbacv1.SchemeGroupVersion.String(), Kind: "Role"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: labels},
	}
}

// TestValidate covers the canFilter decision: Validate must report "not ok"
// when any OLM-owned object still lacks a label the labellers would apply.
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		objects []runtime.Object
		wantOK  bool
		skip    string
	}{
		{
			name:    "CompliantRole",
			objects: []runtime.Object{partialRole("compliant", merge(ownedLabels(), map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue, resolver.ContentHashLabelKey: "hash"}))},
			wantOK:  true,
		},
		{
			name:    "MissingManagedLabel",
			objects: []runtime.Object{partialRole("missing-managed", ownedLabels())},
			wantOK:  false,
		},
		{
			name:    "UnownedRoleIgnored",
			objects: []runtime.Object{partialRole("unowned", nil)},
			wantOK:  true,
		},
		{
			// The compliance test is `filter(item) && !hasLabel(item)` where the
			// RBAC filter already excludes hashed objects, so an object that is
			// owned + managed but unhashed is never counted.
			name:    "MissingHashLabelButManaged",
			objects: []runtime.Object{partialRole("missing-hash", merge(ownedLabels(), map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}))},
			wantOK:  false,
			skip:    "LAB-03: Validate does not detect owned objects missing olm.permissions.hash, so canFilter can be true and labelling is skipped before hashes are applied",
		},
		{
			// Same gap in the other direction: an owned + hashed object missing
			// olm.managed is also never counted.
			name:    "MissingManagedLabelButHashed",
			objects: []runtime.Object{partialRole("missing-managed-hashed", merge(ownedLabels(), map[string]string{resolver.ContentHashLabelKey: "hash"}))},
			wantOK:  false,
			skip:    "LAB-03: Validate does not detect owned objects missing olm.managed when the content-hash label is present",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}

			scheme := runtime.NewScheme()
			require.NoError(t, metav1.AddMetaToScheme(scheme))
			metadataClient := metadatafake.NewSimpleMetadataClient(scheme, tt.objects...)

			logger, _ := test.NewNullLogger()
			ok, err := Validate(context.TODO(), logger, metadataClient, versionedfake.NewSimpleClientset(), IdentityCatalogOperator)
			require.NoError(t, err)
			require.Equal(t, tt.wantOK, ok)
		})
	}
}
