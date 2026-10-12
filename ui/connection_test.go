package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/credentials"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/gnocent/termdevtools/refdata"
)

func TestConnectClusterValidation(t *testing.T) {
	for _, status := range []int{200, 301, 307, 401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
				w.Write([]byte(elasticsearchRoot))
			}))
			defer srv.Close()
			result, err := connectCluster(context.Background(), config.Cluster{URL: srv.URL, AuthType: config.AuthNone, Proxy: "none"}, credentials.Values{}, time.Second, i18n.For(i18n.EN))
			if status == 200 {
				if err != nil || result.Client == nil || result.Target.Distribution != refdata.Elasticsearch {
					t.Fatal(result, err)
				}
				result.Client.CloseIdleConnections()
			} else {
				var he *connectionHTTPError
				if !errors.As(err, &he) || he.StatusCode != status {
					t.Fatal(err)
				}
			}
			if requests != 1 {
				t.Fatal("followed redirect or retried", requests)
			}
		})
	}
	received := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(received); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := connectCluster(ctx, config.Cluster{URL: srv.URL, Proxy: "none"}, credentials.Values{}, time.Second, i18n.For(i18n.EN))
		done <- err
	}()
	<-received
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("validation ignored cancellation")
	}
}
func TestConnectClusterPrefix(t *testing.T) {
	paths := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paths <- r.URL.Path; w.Write([]byte(elasticsearchRoot)) }))
	defer srv.Close()
	result, err := connectCluster(context.Background(), config.Cluster{URL: srv.URL + "/tenant/search", Proxy: "none"}, credentials.Values{}, time.Second, i18n.For(i18n.EN))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Client.CloseIdleConnections()
	if _, err = result.Client.Execute(context.Background(), "GET", "/_cat/indices", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/tenant/search/", "/tenant/search/_cat/indices"} {
		if got := <-paths; got != want {
			t.Fatal(got, want)
		}
	}
}
