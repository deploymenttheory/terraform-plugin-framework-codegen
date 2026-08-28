package testapiserver

import (
	"fmt"
	"strings"
)

// monitorKinds is the closed set the `kind` discriminator admits. A monitor's
// whole shape follows from its kind.
var monitorKinds = []string{"ping", "web", "dns"}

// monitorVariantFields maps each variant-specific field to the one kind it
// belongs to, in a fixed order so the wrong-field error is deterministic when
// a body carries several. `interval` and `name` are common to every kind and
// so are absent here.
var monitorVariantFields = []struct{ field, kind string }{
	{"target_host", "ping"},
	{"web", "web"},
	{"domain", "dns"},
	{"dnssec", "dns"},
}

// validateMonitor is the multi-variant ground truth. It reports the first
// problem it finds, in this order, so the grammar is predictable:
//
//  1. kind absent            -> "field kind is required"
//  2. kind not in the set    -> "field kind must be one of ping, web, dns"
//  3. interval absent        -> "field interval is required"
//     (the learn-from-400 edge: the spec marks interval optional, the server
//     does not; the message names it so the executor can add it and retry)
//  4. a field belonging to another kind is present
//     -> "field <x> is not valid when kind=<k>"
//  5. dnssec present on a dns monitor with no domain
//     -> "field dnssec requires field domain to be set"
//     (the value-conditional co-requirement: dnssec is legal only on a dns
//     monitor -- rule 4 handles the wrong-kind case -- and only once domain is
//     set. Checked before rule 6 so that dnssec-without-domain names dnssec,
//     while domain-without-dnssec names domain: the same missing field, two
//     different truths, which is exactly what triangulation must tease apart.)
//  6. the field this kind requires is absent
//     -> "field <x> is required when kind=<k>"
//     ("field web.url is required when kind=web" when
//     the web block is present but carries no url)
//
// Every sentence begins `field <name> `, so `^field (\S+) ` pulls the offending
// field out of any of them.
func (s *Server) validateMonitor(body map[string]any) string {
	rawKind, ok := body["kind"]
	if !ok || fmt.Sprint(rawKind) == "" {
		return "field kind is required"
	}
	kind := fmt.Sprint(rawKind)
	if !contains(monitorKinds, kind) {
		return "field kind must be one of " + strings.Join(monitorKinds, ", ")
	}

	// The optional-in-the-document-but-really-required field.
	if _, present := body["interval"]; !present {
		return "field interval is required"
	}

	// A field that belongs to a different variant is a conflict, named.
	for _, vf := range monitorVariantFields {
		if vf.kind == kind {
			continue
		}
		if _, present := body[vf.field]; present {
			return fmt.Sprintf("field %s is not valid when kind=%s", vf.field, kind)
		}
	}

	// The value-conditional co-requirement, reachable only for kind=dns since a
	// dnssec on any other kind was refused just above.
	if kind == "dns" {
		if _, hasDNSSEC := body["dnssec"]; hasDNSSEC {
			if _, hasDomain := body["domain"]; !hasDomain {
				return "field dnssec requires field domain to be set"
			}
		}
	}

	return monitorRequiredForKind(body, kind)
}

// monitorRequiredForKind reports the field this kind cannot be created without,
// or "" when it is present. web is special: it is a block, and its url is
// required within it.
func monitorRequiredForKind(body map[string]any, kind string) string {
	switch kind {
	case "ping":
		if _, ok := body["target_host"]; !ok {
			return "field target_host is required when kind=ping"
		}
	case "web":
		web, ok := body["web"].(map[string]any)
		if !ok {
			return "field web is required when kind=web"
		}
		if _, ok := web["url"]; !ok {
			return "field web.url is required when kind=web"
		}
	case "dns":
		if _, ok := body["domain"]; !ok {
			return "field domain is required when kind=dns"
		}
	}
	return ""
}
