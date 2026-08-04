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

	abstractions "github.com/microsoft/kiota-abstractions-go"
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

// ErrEmptyResponse is returned when a fluent SDK call reports success but hands
// back no body. Kiota renders that outcome as (nil, nil), which generated code
// must turn into an error rather than dereference: a create that "succeeded"
// without returning the object leaves nothing to write to state.
var ErrEmptyResponse = stderrors.New("the API returned success with an empty body")

// IsNotFound reports whether err is a 404.
//
// Read uses it to remove a resource from state rather than fail: something
// deleted outside Terraform should produce a plan that recreates it, not an error
// the practitioner cannot clear.
//
// Every generated error model embeds abstractions.ApiError, so the interface
// assertion reaches them all without this package naming any of them.
func IsNotFound(err error) bool {
	var apiErr abstractions.ApiErrorable
	if !stderrors.As(err, &apiErr) {
		return false
	}

	return apiErr.GetStatusCode() == http.StatusNotFound
}

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

	var apiErr abstractions.ApiErrorable
	if !stderrors.As(err, &apiErr) {
		// Transport failures, context cancellation and timeouts land here. There
		// is no API explanation to quote, so the wrapped error is all there is.
		diags.AddError(summary, err.Error())
		return
	}

	// The generated error models embed abstractions.ApiError and carry the API's
	// own explanation in its Message, which err.Error() renders. Kiota does not
	// record the method or path on the error, so unlike the resty SDK's
	// diagnostics the status line names only the status.
	status := apiErr.GetStatusCode()

	detail := err.Error()
	if detail == "" || detail == "error status code received from the API" {
		// Kiota's fallback message restates that there was an error without
		// saying anything the status line does not, so it earns no second line.
		detail = "The API offered no further explanation."
	}

	msg := fmt.Sprintf("The ThousandEyes API returned %d %s.\n\n%s",
		status, http.StatusText(status), detail)

	if hint := hintFor(status, op); hint != "" {
		msg += "\n\n" + hint
	}

	diags.AddError(summary, msg)
}

// hintFor returns advice for the statuses whose cause is not evident from the
// status line. It deliberately says nothing for the rest: a hint that merely
// restates the status trains people to ignore hints.
func hintFor(status int, op Operation) string {
	switch status {
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
