// Package credentials resolves named-context authentication without writing
// secrets. The session controller owns this store on the UI loop.
package credentials

import (
	"os"

	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/esclient"
)

type Field string

const (
	FieldPassword      Field = "password"
	FieldAPIKeyID      Field = "api_key_id"
	FieldAPIKeySecret  Field = "api_key_secret"
	FieldBearerToken   Field = "bearer_token"
	FieldKeyPassphrase Field = "key_passphrase"
)

type Values struct{ Password, APIKeyID, APIKeySecret, BearerToken, KeyPassphrase string }
type cached struct {
	fingerprint [32]byte
	values      Values
}
type Store struct {
	lookupEnv func(string) (string, bool)
	cache     map[string]cached
}

func NewStore(lookupEnv func(string) (string, bool)) *Store {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	return &Store{lookupEnv: lookupEnv, cache: map[string]cached{}}
}
func (s *Store) Resolve(d config.ContextDefinition, needPassphrase bool) (Values, []Field) {
	var v Values
	if saved, ok := s.cache[d.ID()]; ok && saved.fingerprint == d.Fingerprint() {
		v = saved.values
	}
	resolve := func(ref, fallback string) string {
		if ref != "" {
			if value, ok := s.lookupEnv(ref); ok && value != "" {
				return value
			}
		}
		return fallback
	}
	var out Values
	var missing []Field
	switch d.Cluster.AuthType {
	case config.AuthBasic:
		out.Password = resolve(d.Secrets.Password, v.Password)
		if out.Password == "" {
			missing = append(missing, FieldPassword)
		}
	case config.AuthAPIKey:
		out.APIKeyID = d.Cluster.APIKeyID
		if out.APIKeyID == "" {
			out.APIKeyID = v.APIKeyID
		}
		out.APIKeySecret = resolve(d.Secrets.APIKeySecret, v.APIKeySecret)
		out.APIKeyID, out.APIKeySecret = esclient.ResolveAPIKey(out.APIKeyID, out.APIKeySecret)
		if out.APIKeySecret == "" {
			missing = append(missing, FieldAPIKeySecret)
		} else if out.APIKeyID == "" {
			missing = append(missing, FieldAPIKeyID)
		}
	case config.AuthBearer:
		out.BearerToken = resolve(d.Secrets.BearerToken, v.BearerToken)
		if out.BearerToken == "" {
			missing = append(missing, FieldBearerToken)
		}
	case config.AuthMTLS:
		if needPassphrase {
			out.KeyPassphrase = resolve(d.Secrets.KeyPassphrase, v.KeyPassphrase)
			if out.KeyPassphrase == "" {
				missing = append(missing, FieldKeyPassphrase)
			}
		}
	}
	return out, missing
}
func (s *Store) Accept(d config.ContextDefinition, values Values, entered []Field) {
	if len(entered) == 0 {
		return
	}
	old := s.cache[d.ID()]
	if old.fingerprint != d.Fingerprint() {
		old = cached{fingerprint: d.Fingerprint()}
	}
	for _, field := range entered {
		switch field {
		case FieldPassword:
			if d.Cluster.AuthType == config.AuthBasic {
				old.values.Password = values.Password
			}
		case FieldAPIKeyID:
			if d.Cluster.AuthType == config.AuthAPIKey {
				old.values.APIKeyID = values.APIKeyID
			}
		case FieldAPIKeySecret:
			if d.Cluster.AuthType == config.AuthAPIKey {
				old.values.APIKeyID, old.values.APIKeySecret = esclient.ResolveAPIKey(values.APIKeyID, values.APIKeySecret)
			}
		case FieldBearerToken:
			if d.Cluster.AuthType == config.AuthBearer {
				old.values.BearerToken = values.BearerToken
			}
		case FieldKeyPassphrase:
			if d.Cluster.AuthType == config.AuthMTLS {
				old.values.KeyPassphrase = values.KeyPassphrase
			}
		}
	}
	s.cache[d.ID()] = old
}
func (s *Store) Forget(identity string) { delete(s.cache, identity) }
func (s *Store) Reconcile(inventory config.Inventory) {
	keep := map[string][32]byte{}
	for _, d := range inventory.Definitions() {
		keep[d.ID()] = d.Fingerprint()
	}
	for identity, saved := range s.cache {
		if fingerprint, ok := keep[identity]; !ok || fingerprint != saved.fingerprint {
			delete(s.cache, identity)
		}
	}
}
