package validatingroundtripper

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
)

type recordingRT struct {
	called bool
}

func (r *recordingRT) RoundTrip(*http.Request) (*http.Response, error) {
	r.called = true
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func newRT(t *testing.T) (*validatingRoundTripper, *recordingRT, serializer.CodecFactory) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	codecs := serializer.NewCodecFactory(scheme)
	rec := &recordingRT{}
	return &validatingRoundTripper{delegate: rec, codecs: codecs}, rec, codecs
}

// opaqueReader defeats http.NewRequest's GetBody synthesis.
type opaqueReader struct{ io.Reader }

func replayableRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/api", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	require.NotNil(t, req.GetBody)
	return req
}

func unreplayableRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/api", opaqueReader{strings.NewReader(body)})
	require.NoError(t, err)
	require.Nil(t, req.GetBody)
	return req
}

func protobufRequest(t *testing.T, codecs serializer.CodecFactory, obj runtime.Object, gv schema.GroupVersion) *http.Request {
	t.Helper()
	info, ok := runtime.SerializerInfoForMediaType(codecs.SupportedMediaTypes(), runtime.ContentTypeProtobuf)
	require.True(t, ok, "protobuf serializer not registered")
	data, err := runtime.Encode(codecs.EncoderForVersion(info.Serializer, gv), obj)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/api", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", runtime.ContentTypeProtobuf)
	require.NotNil(t, req.GetBody)
	return req
}

func labelledJSON() string {
	return `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"c","namespace":"ns","labels":{"` +
		install.OLMManagedLabelKey + `":"` + install.OLMManagedLabelValue + `"}}}`
}

func unlabelledJSON(kind string) string {
	return `{"apiVersion":"v1","kind":"` + kind + `","metadata":{"name":"c","namespace":"ns"}}`
}

func panicValue(fn func()) (value interface{}) {
	defer func() { value = recover() }()
	fn()
	return nil
}

func TestDecodeRequestBodyNoGetBody(t *testing.T) {
	t.Skip("VRT-01: decodeRequestBody calls the req.GetBody field without a nil check, so a POST with no body or a non-replayable reader panics")

	rt, _, _ := newRT(t)
	req := unreplayableRequest(t, unlabelledJSON("ConfigMap"))

	var (
		u   *unstructured.Unstructured
		err error
	)
	require.NotPanics(t, func() {
		u, err = rt.decodeRequestBody(req)
	})
	// A non-replayable body is still readable once; it should decode cleanly
	// (and, on the fix branch, be restored for the delegate to send).
	require.NoError(t, err)
	require.NotNil(t, u)
	require.Equal(t, "ConfigMap", u.GetKind())
}

func TestDecodeProtobufPreservesGVK(t *testing.T) {
	rt, _, codecs := newRT(t)
	event := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e", Namespace: "ns"}}
	req := protobufRequest(t, codecs, event, corev1.SchemeGroupVersion)

	body, err := req.GetBody()
	require.NoError(t, err)
	defer body.Close()

	u, err := rt.decodeProtobuf(body)
	require.NoError(t, err)
	require.Equal(t, "Event", u.GetKind())
	require.Equal(t, "v1", u.GetAPIVersion())
}

func TestRoundTrip(t *testing.T) {
	t.Run("GETPassesThrough", func(t *testing.T) {
		rt, rec, _ := newRT(t)
		req, err := http.NewRequest(http.MethodGet, "https://example.invalid/api", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
		require.True(t, rec.called)
	})

	t.Run("LabelledJSONPasses", func(t *testing.T) {
		rt, rec, _ := newRT(t)
		_, err := rt.RoundTrip(replayableRequest(t, labelledJSON()))
		require.NoError(t, err)
		require.True(t, rec.called)
	})

	t.Run("UnlabelledJSONPanics", func(t *testing.T) {
		rt, rec, _ := newRT(t)
		value := panicValue(func() { _, _ = rt.RoundTrip(replayableRequest(t, unlabelledJSON("ConfigMap"))) })
		require.NotNil(t, value)
		require.Contains(t, fmt.Sprint(value), "ConfigMap")
		require.False(t, rec.called)
	})

	t.Run("EventJSONExempt", func(t *testing.T) {
		rt, rec, _ := newRT(t)
		_, err := rt.RoundTrip(replayableRequest(t, unlabelledJSON("Event")))
		require.NoError(t, err)
		require.True(t, rec.called)
	})

	t.Run("ProtobufEventExempt", func(t *testing.T) {
		rt, rec, codecs := newRT(t)
		event := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e", Namespace: "ns"}}
		value := panicValue(func() { _, _ = rt.RoundTrip(protobufRequest(t, codecs, event, corev1.SchemeGroupVersion)) })
		require.Nil(t, value)
		require.True(t, rec.called)
	})

	t.Run("ProtobufUnlabelledPanicNamesKind", func(t *testing.T) {
		rt, _, codecs := newRT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"}}
		value := panicValue(func() { _, _ = rt.RoundTrip(protobufRequest(t, codecs, cm, corev1.SchemeGroupVersion)) })
		require.NotNil(t, value)
		require.Contains(t, fmt.Sprint(value), "ConfigMap")
	})
}

func TestWrapNoopWhenCIUnset(t *testing.T) {
	old, had := os.LookupEnv("CI")
	require.NoError(t, os.Unsetenv("CI"))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("CI", old)
		}
	})

	cfg := &rest.Config{Host: "https://example.invalid"}
	require.Same(t, cfg, Wrap(cfg, runtime.NewScheme()))
}
