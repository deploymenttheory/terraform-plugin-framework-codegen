package config

// Secret names are fixed by auth role — they are a contract, not
// configuration. v1 let the workflow and the config each spell secret names
// and a rename broke the pipeline with an error citing the old spelling;
// here there is exactly one definition site.
const (
	SecretToken         = "TFPFGEN_AUTH_TOKEN"
	SecretClientID      = "TFPFGEN_AUTH_CLIENT_ID"
	SecretClientSecret  = "TFPFGEN_AUTH_CLIENT_SECRET"
	SecretUsername      = "TFPFGEN_AUTH_USERNAME"
	SecretPassword      = "TFPFGEN_AUTH_PASSWORD"
	SecretAppID         = "TFPFGEN_AUTH_APP_ID"
	SecretAppPrivateKey = "TFPFGEN_AUTH_APP_PRIVATE_KEY"
)

// RequiredSecrets names the environment variables the given auth method
// reads. An unknown method needs nothing — method validity is the schema
// validator's finding, not this function's.
func RequiredSecrets(method string) []string {
	switch method {
	case AuthBearerToken, AuthAPIKeyHeader:
		return []string{SecretToken}
	case AuthBasic:
		return []string{SecretUsername, SecretPassword}
	case AuthOAuth2ClientCredentials:
		return []string{SecretClientID, SecretClientSecret}
	case AuthGitHubApp:
		return []string{SecretAppID, SecretAppPrivateKey}
	default:
		return nil
	}
}

// MissingSecrets reports every required-but-absent secret at once, using the
// supplied lookup so callers and tests control the environment. Values are
// never returned — presence is the only fact anyone needs.
func MissingSecrets(method string, lookup func(string) (string, bool)) []string {
	var missing []string
	for _, name := range RequiredSecrets(method) {
		if v, ok := lookup(name); !ok || v == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
