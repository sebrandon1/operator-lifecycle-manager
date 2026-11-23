package proxy

import (
	"errors"
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
	listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ listers.ProxyLister = (*fakeProxyLister)(nil)

type fakeProxyLister struct {
	proxy *apiconfigv1.Proxy
	err   error
}

func (f *fakeProxyLister) Get(string) (*apiconfigv1.Proxy, error) {
	return f.proxy, f.err
}

func (f *fakeProxyLister) List(labels.Selector) ([]*apiconfigv1.Proxy, error) {
	if f.proxy == nil {
		return nil, f.err
	}
	return []*apiconfigv1.Proxy{f.proxy}, f.err
}

func newSyncerWithLister(t *testing.T, lister listers.ProxyLister) *Syncer {
	t.Helper()
	logger, _ := test.NewNullLogger()
	return &Syncer{logger: logger, lister: lister}
}

func TestQueryProxyConfig(t *testing.T) {
	t.Run("NotFoundReturnsNilNil", func(t *testing.T) {
		s := newSyncerWithLister(t, &fakeProxyLister{err: apierrors.NewNotFound(schema.GroupResource{Resource: "proxies"}, globalProxyName)})
		env, err := s.QueryProxyConfig()
		require.NoError(t, err)
		require.Nil(t, env)
	})

	t.Run("OtherErrorPropagates", func(t *testing.T) {
		s := newSyncerWithLister(t, &fakeProxyLister{err: errors.New("lister unavailable")})
		env, err := s.QueryProxyConfig()
		require.Error(t, err)
		require.Nil(t, env)
	})

	t.Run("ProxyPresentReturnsEnvVars", func(t *testing.T) {
		proxy := &apiconfigv1.Proxy{
			Status: apiconfigv1.ProxyStatus{
				HTTPProxy:  "http://proxy:3128",
				HTTPSProxy: "https://proxy:3129",
				NoProxy:    ".example.invalid,localhost",
			},
		}
		s := newSyncerWithLister(t, &fakeProxyLister{proxy: proxy})
		env, err := s.QueryProxyConfig()
		require.NoError(t, err)
		require.Equal(t, []corev1.EnvVar{
			{Name: envHTTPProxyName, Value: "http://proxy:3128"},
			{Name: envHTTPSProxyName, Value: "https://proxy:3129"},
			{Name: envNoProxyName, Value: ".example.invalid,localhost"},
		}, env)
	})
}

func TestSyncProxyAndDelete(t *testing.T) {
	s := newSyncerWithLister(t, &fakeProxyLister{})

	require.NoError(t, s.SyncProxy(&apiconfigv1.Proxy{}))
	require.NoError(t, s.SyncProxy(&corev1.ConfigMap{}))
	require.NotPanics(t, func() { s.HandleProxyDelete(&apiconfigv1.Proxy{}) })
	require.NotPanics(t, func() { s.HandleProxyDelete(&corev1.ConfigMap{}) })
}

func TestNoopQuerier(t *testing.T) {
	env, err := NoopQuerier().QueryProxyConfig()
	require.NoError(t, err)
	require.Nil(t, env)
}
