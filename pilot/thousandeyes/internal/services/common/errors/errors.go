// Package errors turns SDK errors into Terraform diagnostics.
//
// It is hand-written and owned by the provider, not generated, because mapping an
// API's failure modes onto messages a practitioner can act on is judgement rather
// than transcription. Generated CRUD calls into it by name.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
)

// Operation names the CRUD phase an error occurred in, so a diagnostic says what
// the provider was attempting rather than only what failed.
type Operation string

const (
	OpCreate Operation = "create"
	OpRead   Operation = "read"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
)

// IsNotFound reports whether err is a 404.
//
// Read uses it to remove a resource from state rather than fail: something
// deleted outside Terraform should produce a plan that recreates it, not an error
// the practitioner cannot clear.
func IsNotFound(err error) bool { return client.IsNotFound(err) }

// Handle appends a diagnostic describing err.
//
// The message leads with the resource and operation, then the API's own
// explanation, then a hint for the failure modes where the cause is not obvious
// from the status alone. A bare "request failed: 403" leaves a practitioner with
// nowhere to go.
func Handle(diags *diag.Diagnostics, resourceType string, op Operation, err error) {
	if err == nil {
		return
	}

	summary := fmt.Sprintf("Unable to %s %s", op, resourceType)

	var apiErr *client.APIError
	if !stderrors.As(err, &apiErr) {
		// Transport failures, context cancellation and timeouts land here. There
		// is no API explanation to quote, so the wrapped error is all there is.
		diags.AddError(summary, err.Error())
		return
	}

	detail := apiErr.Message
	if apiErr.Detail != "" {
		detail = fmt.Sprintf("%s: %s", detail, apiErr.Detail)
	}
	if detail == "" {
		detail = apiErr.Status
	}

	msg := fmt.Sprintf("The ThousandEyes API returned %d %s for %s %s.\n\n%s",
		apiErr.StatusCode, http.StatusText(apiErr.StatusCode), apiErr.Method, apiErr.Endpoint, detail)

	if hint := hintFor(apiErr, op); hint != "" {
		msg += "\n\n" + hint
	}

	diags.AddError(summary, msg)
}

// hintFor returns advice for the statuses whose cause is not evident from the
// status line. It deliberately says nothing for the rest: a hint that merely
// restates the status trains people to ignore hints.
func hintFor(err *client.APIError, op Operation) string {
	switch err.StatusCode {
	case http.StatusUnauthorized:
		return "The bearer token was rejected. ThousandEyes tokens are created by hand " +
			"under Account Settings > Users and Roles and do not expire, so this usually " +
			"means the token was revoked or is malformed rather than that it needs refreshing."

	case http.StatusForbidden:
		return "The token authenticated but lacks permission for this operation, or the " +
			"account group it is scoped to does not contain the object. Check the token " +
			"owner's role and the provider's account_group_id."

	case http.StatusNotFound:
		if op == OpDelete {
			return "The object was already gone. This is usually benign."
		}
		return "The object does not exist in the account group the token is scoped to. " +
			"An object can be invisible rather than absent if account_group_id is wrong."

	case http.StatusTooManyRequests:
		return "The organisation's rate limit was exhausted. The SDK retries these " +
			"automatically, so reaching this error means the limit stayed exhausted for " +
			"the whole retry budget; reduce parallelism with -parallelism."

	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "The API rejected the request body. ThousandEyes validation errors often " +
			"omit the offending field, so compare the configuration against the API " +
			"reference for this endpoint."

	default:
		return ""
	}
}
