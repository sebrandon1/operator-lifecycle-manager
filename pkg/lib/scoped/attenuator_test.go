package scoped

import (
	"errors"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

const (
	attenuatorSA     = "attenuated"
	attenuatorNS     = "ns"
	attenuatorToken  = "s3cr3t-token"
	attenuatorSecret = "attenuated-token"
)

func newAttenuator(t *testing.T, objs ...runtime.Object) *ClientAttenuator {
	t.Helper()

	client := operatorclient.NewClient(
		fake.NewSimpleClientset(objs...),
		apiextensionsfake.NewSimpleClientset(),
		apiregistrationfake.NewSimpleClientset(),
	)
	logger, _ := test.NewNullLogger()
	return NewClientAttenuator(logger, &rest.Config{}, client)
}

func serviceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: attenuatorSA, Namespace: attenuatorNS}}
}

func tokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        attenuatorSecret,
			Namespace:   attenuatorNS,
			Annotations: map[string]string{corev1.ServiceAccountNameKey: attenuatorSA},
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte(attenuatorToken)},
	}
}

func ref() *corev1.ObjectReference {
	return &corev1.ObjectReference{Kind: "ServiceAccount", Name: attenuatorSA, Namespace: attenuatorNS}
}

func TestAttenuateToServiceAccount(t *testing.T) {
	t.Run("NilReferenceReturnsIdentityTransformer", func(t *testing.T) {
		s := newAttenuator(t)
		transformer, err := s.AttenuateToServiceAccount(StaticQuerier(nil))
		require.NoError(t, err)
		require.NotNil(t, transformer)

		in := &rest.Config{Host: "https://example.invalid", Username: "u", Password: "p"}
		out := transformer.TransformConfig(in)
		require.Same(t, in, out)
	})

	t.Run("QuerierErrorPropagates", func(t *testing.T) {
		s := newAttenuator(t)
		_, err := s.AttenuateToServiceAccount(func() (*corev1.ObjectReference, error) {
			return nil, errors.New("querier failed")
		})
		require.Error(t, err)
	})

	t.Run("MissingServiceAccountPropagatesError", func(t *testing.T) {
		s := newAttenuator(t)
		_, err := s.AttenuateToServiceAccount(StaticQuerier(ref()))
		require.Error(t, err)
	})

	t.Run("TokenAppliedAndCredentialsDropped", func(t *testing.T) {
		s := newAttenuator(t, serviceAccount(), tokenSecret())
		transformer, err := s.AttenuateToServiceAccount(StaticQuerier(ref()))
		require.NoError(t, err)
		require.NotNil(t, transformer)

		in := &rest.Config{
			Host:        "https://example.invalid",
			Username:    "user",
			Password:    "password",
			BearerToken: "old-token",
		}
		out := transformer.TransformConfig(in)

		require.NotSame(t, in, out)
		require.Equal(t, attenuatorToken, out.BearerToken)
		require.Empty(t, out.Username)
		require.Empty(t, out.Password)
		require.Equal(t, in.Host, out.Host)
		// The input config is not mutated.
		require.Equal(t, "old-token", in.BearerToken)
	})
}
