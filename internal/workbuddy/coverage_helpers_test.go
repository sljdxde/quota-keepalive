package workbuddy

import (
	"context"
	"io"
	"net/http"
	"strings"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingReadCloser struct {
	err error
}

func (r *failingReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r *failingReadCloser) Close() error {
	return nil
}

type closeHookReadCloser struct {
	io.Reader
	closeFn func()
}

func (r *closeHookReadCloser) Close() error {
	r.closeFn()
	return nil
}

func testJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type hookManagedStore struct {
	load    func(context.Context) ([]byte, error)
	save    func(context.Context, []byte) error
	healthy func(context.Context) error
	reauth  func(context.Context) error
}

func (s *hookManagedStore) Load(ctx context.Context) ([]byte, error) {
	return s.load(ctx)
}

func (s *hookManagedStore) Save(ctx context.Context, payload []byte) error {
	if s.save == nil {
		return nil
	}
	return s.save(ctx, payload)
}

func (s *hookManagedStore) MarkHealthy(ctx context.Context) error {
	if s.healthy == nil {
		return nil
	}
	return s.healthy(ctx)
}

func (s *hookManagedStore) MarkReauthRequired(ctx context.Context) error {
	if s.reauth == nil {
		return nil
	}
	return s.reauth(ctx)
}
