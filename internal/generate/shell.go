// The provider shell: the support packages generated code calls into.
//
// The shell was hand-written once, in the kiota pilot, and proven there by live
// acceptance runs; `provider scaffold` re-emits that proven tree from templates
// so a new provider starts from it rather than from a blank page, and so a fix
// to the shell lands in every provider by regeneration rather than by hand.
// The templates live in internal/templates/shell, mirroring the paths they are
// emitted to.

package generate

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/templates"
)

// ShellParams is everything that varies between one provider's shell and
// another's. Deliberately small: the shell is a proven tree, and every
// parameter is one more way an emitted provider can differ from the tree the
// evidence was gathered under.
type ShellParams struct {
	// Module is the provider's Go module path, e.g.
	// "github.com/deploymenttheory/terraform-provider-thousandeyes".
	Module string
	// Name is the provider's registry name, e.g. "thousandeyes". It is the
	// provider type name in HCL and the last segment of the registry address.
	Name string
	// EnvPrefix prefixes the environment variables the provider reads, e.g.
	// "THOUSANDEYES" for THOUSANDEYES_BEARER_TOKEN.
	EnvPrefix string
	// RegistryOrg is the registry namespace, e.g. "deploymenttheory" in
	// registry.terraform.io/deploymenttheory/thousandeyes.
	RegistryOrg string
	// ClientClass is the SDK client's type name, e.g. "ThousandEyesClient".
	ClientClass string
	// DisplayName is the API's name as a person writes it, e.g. "ThousandEyes".
	// Every sentence a practitioner reads names the API through this, so a
	// provider for some other API does not describe itself as the pilot's.
	DisplayName string
	// DefaultEndpoint is the base URL to fall back to, or empty when the API
	// has no single host and the practitioner must name one.
	DefaultEndpoint string

	// AuthMethod is the resolved authentication method, for the one place a
	// template states which it is rather than branching on it.
	AuthMethod string
	// IsBearer, IsClientCredentials and IsUsernamePassword select the auth
	// branch. Three booleans rather than one method string a template compares
	// against, because a template that compares is a template that can compare
	// wrongly, and a misspelt method name in an {{ if eq }} silently emits the
	// other branch.
	IsBearer            bool
	IsClientCredentials bool
	IsUsernamePassword  bool
	// TokenURL is the client-credentials token endpoint, empty for every other
	// method. Validation has already refused a clientCredentials block without
	// one.
	TokenURL string
	// Scopes is the requested scopes already rendered as the Go expression the
	// template drops into a struct literal: `[]string{"a", "b"}`, or `nil` when
	// none are declared. Rendered here rather than in the template, because
	// quoting a string list is code and templates in this repo carry none.
	Scopes string
}

// ShellParamsFrom derives the template parameters from a provider block.
//
// Derives rather than accepts: every value here is already stated in the
// blueprint, and a flag restating one would be a second place for it to be
// wrong. The registry org falls out of the module path's second segment, which
// holds for any github.com/<org>/... module.
func ShellParamsFrom(p blueprint.Provider) (ShellParams, error) {
	if p.Name == "" || p.GoModule == "" {
		return ShellParams{}, fmt.Errorf("the provider block must carry name and goModule")
	}

	segments := strings.Split(p.GoModule, "/")
	if len(segments) < 2 {
		return ShellParams{}, fmt.Errorf(
			"cannot derive a registry namespace from goModule %q: expected host/org/...", p.GoModule)
	}

	clientClass := p.SDK.ClientType
	clientClass = strings.TrimPrefix(clientClass, "*")
	if i := strings.LastIndex(clientClass, "."); i >= 0 {
		clientClass = clientClass[i+1:]
	}
	if clientClass == "" {
		return ShellParams{}, fmt.Errorf("the provider block's sdk.clientType is empty")
	}

	displayName := p.DisplayName
	if displayName == "" {
		displayName = titleCase(p.Name)
	}

	method := p.Auth.Resolved()

	return ShellParams{
		Module:          p.GoModule,
		Name:            p.Name,
		EnvPrefix:       envPrefix(p.Name),
		RegistryOrg:     segments[1],
		ClientClass:     clientClass,
		DisplayName:     displayName,
		DefaultEndpoint: p.APIEndpoint,

		AuthMethod:          string(method),
		IsBearer:            method == blueprint.AuthBearerToken,
		IsClientCredentials: method == blueprint.AuthClientCredentials,
		IsUsernamePassword:  method == blueprint.AuthUsernamePassword,
		TokenURL:            p.Auth.TokenURL,
		Scopes:              goStringSlice(p.Auth.Scopes),
	}, nil
}

// titleCase upper-cases the first letter, which is the whole of the fallback:
// a provider whose blueprint states no display name gets "Thousandeyes" from
// "thousandeyes", and the blueprint's displayName is how it gets "ThousandEyes".
// Guessing harder would only produce a different wrong answer more confidently.
func titleCase(name string) string {
	for i, r := range name {
		return string(unicode.ToUpper(r)) + name[i+len(string(r)):]
	}
	return name
}

// goStringSlice renders a string list as the Go expression for it, so the
// template that needs one drops in a value rather than quoting anything.
func goStringSlice(values []string) string {
	if len(values) == 0 {
		return "nil"
	}

	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}

	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// envPrefix upper-cases a provider name into an environment-variable prefix,
// with every character an environment variable cannot carry as an underscore:
// "jamf-pro" becomes "JAMF_PRO".
func envPrefix(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// shellView is what a shell template renders against: the parameters plus the
// generated header, which is per-run rather than per-provider.
type shellView struct {
	ShellParams
	Header string
}

// BuildShell renders every shell template, without writing anything.
//
// blueprintPath and blueprintSHA256 go into each file's generated header, the
// same way provider generate's do, so a reader of an emitted shell file is
// pointed at the provider block it came from.
func BuildShell(params ShellParams, blueprintPath, blueprintSHA256 string) (Fileset, error) {
	view := shellView{
		ShellParams: params,
		Header:      GeneratedHeader(blueprintPath, blueprintSHA256),
	}

	var plan Fileset

	err := fs.WalkDir(templates.ShellFS, "shell", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".tmpl") {
			return nil
		}

		src, err := fs.ReadFile(templates.ShellFS, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}

		// Parsed one file at a time rather than as a set, because the shell
		// tree repeats base names (two errors.go.tmpl) and a shared namespace
		// would let one silently shadow the other.
		tmpl, err := template.New(p).Parse(string(src))
		if err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, view); err != nil {
			return fmt.Errorf("rendering %s: %w", p, err)
		}

		out := strings.TrimSuffix(strings.TrimPrefix(p, "shell/"), ".tmpl")

		content := buf.Bytes()
		if strings.HasSuffix(p, ".go.tmpl") {
			content, err = formatGo(content)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}

		plan.Files = append(plan.Files, File{Path: out, Content: content})

		return nil
	})
	if err != nil {
		return Fileset{}, err
	}

	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })

	return plan, nil
}

// ProviderBlockDigest is the digest a scaffolded file's header records: the
// canonical encoding of the loaded document, computed the same way provider
// generate's header digest is, so reformatting the provider block without
// changing its meaning does not rewrite the whole shell.
func ProviderBlockDigest(bp blueprint.Blueprint) string {
	return blueprintDigest(bp)
}
