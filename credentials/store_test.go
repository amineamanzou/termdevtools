package credentials

import (
	"encoding/base64"
	"testing"

	"github.com/gnocent/termdevtools/config"
)

func basic(name string) config.ContextDefinition {
	return config.ContextDefinition{Name: name, Cluster: config.Cluster{URL: "http://localhost", AuthType: config.AuthBasic, Username: "user"}, Secrets: config.SecretRefs{Password: "PASSWORD"}}
}
func TestResolveSecretPrecedence(t *testing.T) {
	env := map[string]string{}
	store := NewStore(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	d := basic("a")
	v, missing := store.Resolve(d, false)
	if v.Password != "" || len(missing) != 1 || missing[0] != FieldPassword {
		t.Fatal(v, missing)
	}
	store.Accept(d, Values{Password: "manual"}, []Field{FieldPassword})
	env["PASSWORD"] = "environment"
	v, missing = store.Resolve(d, false)
	if v.Password != "environment" || len(missing) != 0 {
		t.Fatal(v, missing)
	}
	store.Accept(d, v, nil)
	env["PASSWORD"] = ""
	v, _ = store.Resolve(d, false)
	if v.Password != "manual" {
		t.Fatal("environment overwrote manual cache")
	}
	fresh := NewStore(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	env["PASSWORD"] = "environment"
	v, _ = fresh.Resolve(d, false)
	fresh.Accept(d, v, nil)
	delete(env, "PASSWORD")
	_, missing = fresh.Resolve(d, false)
	if len(missing) != 1 {
		t.Fatal("environment cached")
	}
	store.Forget(d.ID())
	_, missing = store.Resolve(d, false)
	if len(missing) != 1 {
		t.Fatal("refused credential retained")
	}
}
func TestSecretCacheIsolation(t *testing.T) {
	store := NewStore(func(string) (string, bool) { return "", false })
	a, b := basic("a"), basic("b")
	store.Accept(a, Values{Password: "manual"}, []Field{FieldPassword})
	if v, miss := store.Resolve(b, false); v.Password != "" || len(miss) != 1 {
		t.Fatal("shared credentials", v)
	}
	if v, miss := store.Resolve(a, false); v.Password != "manual" || len(miss) != 0 {
		t.Fatal(v, miss)
	}
	a.Cluster.URL = "http://other"
	if v, _ := store.Resolve(a, false); v.Password != "" {
		t.Fatal("reused changed profile")
	}
}
func TestSecretCacheReconcile(t *testing.T) {
	for _, change := range []string{"labels", "url", "env", "tls", "proxy", "removed"} {
		t.Run(change, func(t *testing.T) {
			inv, e := config.ParseContexts([]byte("contexts:\n  - {name: a, url: http://localhost, auth_type: basic, username: user, password_env: PASSWORD}\n"), "test.yaml")
			if e != nil {
				t.Fatal(e)
			}
			d, _ := inv.Lookup("a")
			store := NewStore(func(string) (string, bool) { return "", false })
			store.Accept(d, Values{Password: "manual"}, []Field{FieldPassword})
			yaml := "contexts:\n  - {name: a, url: http://localhost, auth_type: basic, username: user, password_env: PASSWORD"
			switch change {
			case "labels":
				yaml += ", labels: {env: changed}"
			case "url":
				yaml = "contexts:\n  - {name: a, url: http://other, auth_type: basic, username: user, password_env: PASSWORD"
			case "env":
				yaml = "contexts:\n  - {name: a, url: http://localhost, auth_type: basic, username: user, password_env: OTHER"
			case "tls":
				yaml += ", tls: {verify: false}"
			case "proxy":
				yaml += ", proxy: none"
			}
			yaml += "}\n"
			if change == "removed" {
				yaml = "contexts: []"
			}
			next, e := config.ParseContexts([]byte(yaml), "test.yaml")
			if e != nil {
				t.Fatal(e)
			}
			store.Reconcile(next)
			v, _ := store.Resolve(d, false)
			if (v.Password == "manual") != (change == "labels") {
				t.Fatal(change, v)
			}
		})
	}
}
func TestResolveAPIKeyIdentity(t *testing.T) {
	env := base64.StdEncoding.EncodeToString([]byte("key-id:key-secret"))
	s := NewStore(func(string) (string, bool) { return env, true })
	d := config.ContextDefinition{Name: "a", Cluster: config.Cluster{URL: "http://localhost", AuthType: config.AuthAPIKey}, Secrets: config.SecretRefs{APIKeySecret: "API_KEY"}}
	v, missing := s.Resolve(d, false)
	if len(missing) != 0 || v.APIKeyID != "key-id" || v.APIKeySecret != "key-secret" {
		t.Fatal(v, missing)
	}
	env = "plain-secret"
	_, missing = s.Resolve(d, false)
	if len(missing) != 1 || missing[0] != FieldAPIKeyID {
		t.Fatal(missing)
	}
	env = ""
	s.Accept(d, Values{APIKeySecret: base64.StdEncoding.EncodeToString([]byte("manual-id:manual-secret"))}, []Field{FieldAPIKeySecret})
	v, missing = s.Resolve(d, false)
	if len(missing) != 0 || v.APIKeyID != "manual-id" || v.APIKeySecret != "manual-secret" {
		t.Fatal(v, missing)
	}
}
func TestPassphraseOnlyWhenEncrypted(t *testing.T) {
	s := NewStore(func(string) (string, bool) { return "", false })
	d := config.ContextDefinition{Name: "a", Cluster: config.Cluster{AuthType: config.AuthMTLS}}
	if _, missing := s.Resolve(d, false); len(missing) != 0 {
		t.Fatal(missing)
	}
	if _, missing := s.Resolve(d, true); len(missing) != 1 || missing[0] != FieldKeyPassphrase {
		t.Fatal(missing)
	}
	s.Accept(d, Values{KeyPassphrase: "passphrase"}, []Field{FieldKeyPassphrase})
	if v, missing := s.Resolve(d, true); len(missing) != 0 || v.KeyPassphrase != "passphrase" {
		t.Fatal(v, missing)
	}
}
