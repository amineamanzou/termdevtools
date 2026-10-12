package ui

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/credentials"
	"github.com/gnocent/termdevtools/esclient"
	"github.com/gnocent/termdevtools/i18n"
)

type connectionHTTPError struct {
	StatusCode int
	message    string
}

func (e *connectionHTTPError) Error() string { return e.message }

type connectionError struct {
	cause   error
	message string
}

func (e *connectionError) Error() string { return e.message }
func (e *connectionError) Unwrap() error { return e.cause }

// connectCluster prepares a client without changing configuration, focus or
// session state. The caller supplies a bounded validation context.
func connectCluster(ctx context.Context, cl config.Cluster, values credentials.Values, timeout time.Duration, msgs *i18n.Strings) (ConnectResult, error) {
	if cl.URL == "" {
		return ConnectResult{}, fmt.Errorf("%s", msgs.ErrURLRequired)
	}
	if parsed, e := url.Parse(cl.URL); e == nil && parsed.User != nil {
		return ConnectResult{}, fmt.Errorf("%s", msgs.ErrURLCredentials)
	}
	if cl.AuthType == config.AuthAPIKey {
		if values.APIKeyID != "" {
			cl.APIKeyID = values.APIKeyID
		}
		cl.APIKeyID, values.APIKeySecret = esclient.ResolveAPIKey(cl.APIKeyID, values.APIKeySecret)
		if cl.APIKeyID == "" {
			return ConnectResult{}, fmt.Errorf("%s", msgs.ErrAPIKeyIDRequired)
		}
	}
	params := esclient.Params{URL: cl.URL, AuthType: cl.AuthType, Username: cl.Username, Password: values.Password, APIKeyID: cl.APIKeyID, APIKeySecret: values.APIKeySecret, BearerToken: values.BearerToken, Verify: cl.TLS.Verify, CAFile: cl.TLS.CAFile, ClientCert: cl.TLS.ClientCert, ClientKey: cl.TLS.ClientKey, KeyPassphrase: values.KeyPassphrase, Proxy: cl.Proxy, Timeout: timeout}
	client, e := esclient.New(params)
	if e != nil {
		return ConnectResult{}, &connectionError{e, fmt.Sprintf(msgs.ErrConnectFailedFmt, e)}
	}
	success := false
	defer func() {
		if !success {
			client.CloseIdleConnections()
		}
	}()
	proxy := client.Proxy()
	result, e := client.Execute(ctx, "GET", "/", nil)
	if e != nil {
		return ConnectResult{}, &connectionError{e, withProxyHint(fmt.Sprintf(msgs.ErrConnectFailedFmt, e), proxy, msgs)}
	}
	if result.StatusCode >= 300 {
		message := fmt.Sprintf(msgs.ErrClusterHTTPFmt, result.StatusCode)
		if location := result.Headers.Get("Location"); location != "" {
			message += " → " + location
		}
		return ConnectResult{}, &connectionHTTPError{result.StatusCode, withProxyHint(message, proxy, msgs)}
	}
	target, warning := resolveTarget(result.Body, cl, msgs)
	if proxy.URL != "" {
		notice := fmt.Sprintf(msgs.InfoProxyFmt, proxy.URL, proxyOrigin(proxy))
		if warning != "" {
			notice = warning + " · " + notice
		}
		warning = notice
	}
	success = true
	return ConnectResult{Client: client, Cluster: cl, DisplayUser: displayUserFor(cl, msgs), Target: target, Warning: warning}, nil
}
