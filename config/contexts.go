package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var contextName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SecretRefs struct {
	Password      string `yaml:"password_env"`
	APIKeySecret  string `yaml:"api_key_secret_env"`
	BearerToken   string `yaml:"bearer_token_env"`
	KeyPassphrase string `yaml:"key_passphrase_env"`
}
type ContextDefinition struct {
	Name    string
	Cluster Cluster
	Secrets SecretRefs
	Labels  map[string]string
}
type Inventory struct{ definitions []ContextDefinition }
type ContextFiles struct{ Queries, Variables string }

func copyDefinition(d ContextDefinition) ContextDefinition {
	if d.Labels != nil {
		labels := make(map[string]string, len(d.Labels))
		for k, v := range d.Labels {
			labels[k] = v
		}
		d.Labels = labels
	}
	return d
}
func (i Inventory) Definitions() []ContextDefinition {
	out := make([]ContextDefinition, len(i.definitions))
	for n, d := range i.definitions {
		out[n] = copyDefinition(d)
	}
	return out
}
func (i Inventory) Lookup(name string) (ContextDefinition, bool) {
	for _, d := range i.definitions {
		if d.Name == name {
			return copyDefinition(d), true
		}
	}
	return ContextDefinition{}, false
}
func (d ContextDefinition) ID() string { return "named:" + d.Name }
func (d ContextDefinition) Fingerprint() [32]byte {
	b, _ := json.Marshal(struct {
		Cluster Cluster
		Secrets SecretRefs
	}{d.Cluster, d.Secrets})
	return sha256.Sum256(b)
}
func ContextsPath() (string, error) {
	dir, e := ConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(dir, "contexts.yaml"), nil
}
func ContextWorkPaths(name string) (ContextFiles, error) {
	if !contextName.MatchString(name) {
		return ContextFiles{}, fmt.Errorf("invalid context name")
	}
	dir, e := ConfigDir()
	if e != nil {
		return ContextFiles{}, e
	}
	sum := sha256.Sum256([]byte("named:" + name))
	base := filepath.Join(dir, "contexts", hex.EncodeToString(sum[:]))
	return ContextFiles{filepath.Join(base, "queries.txt"), filepath.Join(base, "variables.txt")}, nil
}
func LoadContexts(path string) (Inventory, error) {
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return Inventory{}, nil
	}
	if e != nil {
		return Inventory{}, fmt.Errorf("%s: cannot read contexts: %w", path, e)
	}
	return ParseContexts(data, path)
}

// Validate nodes before Decode: yaml.v3 otherwise coerces scalar types and its
// diagnostic messages can repeat rejected values, including accidental secrets.
func ParseContexts(data []byte, source string) (Inventory, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	e := decoder.Decode(&doc)
	if e == io.EOF {
		return Inventory{}, nil
	}
	if e != nil {
		return Inventory{}, fmt.Errorf("%s: invalid YAML", source)
	}
	var extra yaml.Node
	if e = decoder.Decode(&extra); e != io.EOF {
		return Inventory{}, fmt.Errorf("%s: expected one YAML document", source)
	}
	if len(doc.Content) == 0 {
		return Inventory{}, nil
	}
	fail := func(n *yaml.Node, ctx, reason string) error {
		if ctx != "" {
			ctx = " context " + ctx
		}
		return fmt.Errorf("%s:%d:%s %s", source, n.Line, ctx, reason)
	}
	seen := map[*yaml.Node]bool{}
	var validate func(*yaml.Node, string, string) error
	validate = func(n *yaml.Node, schema, ctx string) error {
		if seen[n] {
			return fail(n, ctx, "cyclic YAML alias")
		}
		seen[n] = true
		defer delete(seen, n)
		if n.Kind == yaml.AliasNode {
			return validate(n.Alias, schema, ctx)
		}
		if schema == "string" {
			if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
				return fail(n, ctx, "expected text")
			}
			return nil
		}
		if schema == "bool" {
			if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
				return fail(n, ctx, "expected boolean")
			}
			return nil
		}
		if schema == "contexts" {
			if n.Kind != yaml.SequenceNode {
				return fail(n, ctx, "expected context list")
			}
			for _, child := range n.Content {
				if e := validate(child, "context", ""); e != nil {
					return e
				}
			}
			return nil
		}
		if n.Kind != yaml.MappingNode {
			return fail(n, ctx, "expected mapping")
		}
		if schema == "context" {
			for x := 0; x < len(n.Content); x += 2 {
				if n.Content[x].Value == "name" && contextName.MatchString(n.Content[x+1].Value) {
					ctx = n.Content[x+1].Value
				}
			}
		}
		keys := map[string]bool{}
		for x := 0; x < len(n.Content); x += 2 {
			key, value := n.Content[x], n.Content[x+1]
			if key.Tag != "!!str" || key.Value == "<<" {
				return fail(key, ctx, "invalid mapping key or YAML merge")
			}
			if keys[key.Value] {
				return fail(key, ctx, "duplicate YAML field")
			}
			keys[key.Value] = true
			next := "string"
			switch schema {
			case "root":
				if key.Value != "contexts" {
					return fail(key, ctx, "unknown inventory field")
				}
				next = "contexts"
			case "context":
				switch key.Value {
				case "name", "url", "auth_type", "username", "api_key_id", "proxy", "distribution", "version", "password_env", "api_key_secret_env", "bearer_token_env", "key_passphrase_env":
				case "tls":
					next = "tls"
				case "labels":
					next = "labels"
				default:
					return fail(key, ctx, "unknown context field (plaintext secrets are forbidden)")
				}
			case "tls":
				switch key.Value {
				case "verify":
					next = "bool"
				case "ca_file", "client_cert", "client_key":
				default:
					return fail(key, ctx, "unknown TLS field")
				}
			case "labels":
			default:
				return fail(key, ctx, "invalid mapping")
			}
			if e := validate(value, next, ctx); e != nil {
				return e
			}
		}
		return nil
	}
	root := doc.Content[0]
	if root.Tag == "!!null" && strings.TrimSpace(root.Value) == "" {
		return Inventory{}, nil
	}
	if e := validate(root, "root", ""); e != nil {
		return Inventory{}, e
	}
	var list *yaml.Node
	for x := 0; x < len(root.Content); x += 2 {
		if root.Content[x].Value == "contexts" {
			list = root.Content[x+1]
		}
	}
	if list == nil {
		return Inventory{}, nil
	}
	for list.Kind == yaml.AliasNode {
		list = list.Alias
	}
	result := Inventory{}
	names := map[string]bool{}
	for _, node := range list.Content {
		var wire struct {
			Name    string            `yaml:"name"`
			Cluster Cluster           `yaml:",inline"`
			Secrets SecretRefs        `yaml:",inline"`
			Labels  map[string]string `yaml:"labels"`
		}
		wire.Cluster.AuthType = AuthNone
		wire.Cluster.TLS.Verify = true
		if e := node.Decode(&wire); e != nil {
			return Inventory{}, fail(node, "", "invalid context definition")
		}
		name := wire.Name
		ctx := ""
		if contextName.MatchString(name) {
			ctx = name
		} else {
			return Inventory{}, fail(node, "", "invalid context name (1–64 ASCII characters)")
		}
		if names[name] {
			return Inventory{}, fail(node, ctx, "duplicate context name")
		}
		names[name] = true
		cl, refs := wire.Cluster, wire.Secrets
		u, e := url.Parse(cl.URL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return Inventory{}, fail(node, ctx, "invalid cluster URL")
		}
		switch cl.AuthType {
		case AuthNone, AuthBasic, AuthAPIKey, AuthBearer, AuthMTLS:
		default:
			return Inventory{}, fail(node, ctx, "invalid authentication type")
		}
		if cl.AuthType == AuthBasic && strings.TrimSpace(cl.Username) == "" {
			return Inventory{}, fail(node, ctx, "basic authentication requires username")
		}
		if cl.AuthType == AuthMTLS && (u.Scheme != "https" || cl.TLS.ClientCert == "" || cl.TLS.ClientKey == "") {
			return Inventory{}, fail(node, ctx, "mTLS requires HTTPS and certificate/key paths")
		}
		for _, ref := range []struct{ value, auth string }{{refs.Password, AuthBasic}, {refs.APIKeySecret, AuthAPIKey}, {refs.BearerToken, AuthBearer}, {refs.KeyPassphrase, AuthMTLS}} {
			if ref.value != "" && (!envName.MatchString(ref.value) || cl.AuthType != ref.auth) {
				return Inventory{}, fail(node, ctx, "invalid or inapplicable environment reference")
			}
		}
		if cl.Proxy != "" && cl.Proxy != "none" {
			p, e := url.Parse(cl.Proxy)
			if e != nil || p.Hostname() == "" || p.User != nil || (p.Scheme != "http" && p.Scheme != "socks5" && p.Scheme != "socks5h") {
				return Inventory{}, fail(node, ctx, "invalid proxy setting")
			}
		}
		result.definitions = append(result.definitions, ContextDefinition{name, cl, refs, wire.Labels})
	}
	sort.Slice(result.definitions, func(a, b int) bool { return result.definitions[a].Name < result.definitions[b].Name })
	return result, nil
}
