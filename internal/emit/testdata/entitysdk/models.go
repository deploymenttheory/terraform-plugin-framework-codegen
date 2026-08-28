// A hand-written stand-in for the kiota-generated models package: the
// read interfaces, concrete structs, constructors, and one generated
// enumeration with its parse companion.
package models

import (
	"errors"
	"time"
)

// HttpServerKind is the generated enumeration.
type HttpServerKind int

// Enumeration members.
const (
	BASIC_HTTPSERVERKIND HttpServerKind = iota
	ADVANCED_HTTPSERVERKIND
)

// String spells the member the way the API does.
func (e HttpServerKind) String() string {
	return [...]string{"basic", "advanced"}[e]
}

// ParseHttpServerKind parses the wire spelling, kiota-shaped.
func ParseHttpServerKind(v string) (any, error) {
	result := BASIC_HTTPSERVERKIND
	switch v {
	case "basic":
		result = BASIC_HTTPSERVERKIND
	case "advanced":
		result = ADVANCED_HTTPSERVERKIND
	default:
		return nil, errors.New("unknown HttpServerKind value: " + v)
	}
	return &result, nil
}

// HttpServerProtocol is a second generated enumeration, carried in
// slices.
type HttpServerProtocol int

// Enumeration members.
const (
	HTTP_HTTPSERVERPROTOCOL HttpServerProtocol = iota
	HTTPS_HTTPSERVERPROTOCOL
)

// String spells the member the way the API does.
func (e HttpServerProtocol) String() string {
	return [...]string{"http", "https"}[e]
}

// ParseHttpServerProtocol parses the wire spelling, kiota-shaped.
func ParseHttpServerProtocol(v string) (any, error) {
	result := HTTP_HTTPSERVERPROTOCOL
	switch v {
	case "http":
		result = HTTP_HTTPSERVERPROTOCOL
	case "https":
		result = HTTPS_HTTPSERVERPROTOCOL
	default:
		return nil, errors.New("unknown HttpServerProtocol value: " + v)
	}
	return &result, nil
}

// HttpServerable is the read interface.
type HttpServerable interface {
	GetId() *string
	SetId(*string)
	GetName() *string
	SetName(*string)
	GetEnabled() *bool
	SetEnabled(*bool)
	GetPort() *int32
	SetPort(*int32)
	GetRatio() *float64
	SetRatio(*float64)
	GetKind() *HttpServerKind
	SetKind(*HttpServerKind)
	GetTags() []string
	SetTags([]string)
	GetProtocols() []HttpServerProtocol
	SetProtocols([]HttpServerProtocol)
	GetSeed() *string
	SetSeed(*string)
	GetSettings() HttpServerSettingsable
	SetSettings(HttpServerSettingsable)
	GetRules() []HttpServerRuleable
	SetRules([]HttpServerRuleable)
	GetDescription() *string
	SetDescription(*string)
}

// HttpServer is the concrete model.
type HttpServer struct {
	id          *string
	name        *string
	enabled     *bool
	port        *int32
	ratio       *float64
	kind        *HttpServerKind
	tags        []string
	protocols   []HttpServerProtocol
	seed        *string
	settings    HttpServerSettingsable
	rules       []HttpServerRuleable
	description *string
	ownerId     *string
}

// NewHttpServer constructs an empty model.
func NewHttpServer() *HttpServer { return &HttpServer{} }

func (m *HttpServer) GetId() *string                       { return m.id }
func (m *HttpServer) SetId(v *string)                      { m.id = v }
func (m *HttpServer) GetName() *string                     { return m.name }
func (m *HttpServer) SetName(v *string)                    { m.name = v }
func (m *HttpServer) GetEnabled() *bool                    { return m.enabled }
func (m *HttpServer) SetEnabled(v *bool)                   { m.enabled = v }
func (m *HttpServer) GetPort() *int32                      { return m.port }
func (m *HttpServer) SetPort(v *int32)                     { m.port = v }
func (m *HttpServer) GetRatio() *float64                   { return m.ratio }
func (m *HttpServer) SetRatio(v *float64)                  { m.ratio = v }
func (m *HttpServer) GetKind() *HttpServerKind             { return m.kind }
func (m *HttpServer) SetKind(v *HttpServerKind)            { m.kind = v }
func (m *HttpServer) GetTags() []string                    { return m.tags }
func (m *HttpServer) SetTags(v []string)                   { m.tags = v }
func (m *HttpServer) GetProtocols() []HttpServerProtocol   { return m.protocols }
func (m *HttpServer) SetProtocols(v []HttpServerProtocol)  { m.protocols = v }
func (m *HttpServer) GetSeed() *string                     { return m.seed }
func (m *HttpServer) SetSeed(v *string)                    { m.seed = v }
func (m *HttpServer) GetSettings() HttpServerSettingsable  { return m.settings }
func (m *HttpServer) SetSettings(v HttpServerSettingsable) { m.settings = v }
func (m *HttpServer) GetRules() []HttpServerRuleable       { return m.rules }
func (m *HttpServer) SetRules(v []HttpServerRuleable)      { m.rules = v }
func (m *HttpServer) GetDescription() *string              { return m.description }
func (m *HttpServer) SetDescription(v *string)             { m.description = v }

// SetOwnerId writes a property the request carries and no response answers.
func (m *HttpServer) SetOwnerId(v *string) { m.ownerId = v }

// HttpServerSettingsable is the nested object's read interface.
type HttpServerSettingsable interface {
	GetRetries() *int64
	SetRetries(*int64)
	GetTrace() *bool
	SetTrace(*bool)
}

// HttpServerSettings is the nested object's concrete model.
type HttpServerSettings struct {
	retries *int64
	trace   *bool
}

// NewHttpServerSettings constructs an empty nested model.
func NewHttpServerSettings() *HttpServerSettings { return &HttpServerSettings{} }

func (m *HttpServerSettings) GetRetries() *int64  { return m.retries }
func (m *HttpServerSettings) SetRetries(v *int64) { m.retries = v }
func (m *HttpServerSettings) GetTrace() *bool     { return m.trace }
func (m *HttpServerSettings) SetTrace(v *bool)    { m.trace = v }

// HttpServerRuleable is the list element's read interface.
type HttpServerRuleable interface {
	GetPattern() *string
	SetPattern(*string)
}

// HttpServerRule is the list element's concrete model.
type HttpServerRule struct {
	pattern *string
}

// NewHttpServerRule constructs an empty element.
func NewHttpServerRule() *HttpServerRule { return &HttpServerRule{} }

func (m *HttpServerRule) GetPattern() *string  { return m.pattern }
func (m *HttpServerRule) SetPattern(v *string) { m.pattern = v }

// HttpServerCollectionResponseable is the list envelope's read interface:
// a wrapped list whose single slice getter is named for the "http_servers"
// wire key — the ThousandEyes Tagsable shape, not a generic GetValue.
type HttpServerCollectionResponseable interface {
	GetHttpServers() []HttpServerable
}

// HttpServerCollectionResponse is the list envelope.
type HttpServerCollectionResponse struct {
	httpServers []HttpServerable
}

// GetHttpServers answers the elements, keyed on the wire envelope name.
func (m *HttpServerCollectionResponse) GetHttpServers() []HttpServerable { return m.httpServers }

// HttpServerRestartRequestable is the invocation body's interface.
type HttpServerRestartRequestable interface {
	GetMode() *string
	SetMode(*string)
}

// HttpServerRestartRequest is the invocation body.
type HttpServerRestartRequest struct {
	mode *string
}

// NewHttpServerRestartRequest constructs an empty body.
func NewHttpServerRestartRequest() *HttpServerRestartRequest { return &HttpServerRestartRequest{} }

func (m *HttpServerRestartRequest) GetMode() *string  { return m.mode }
func (m *HttpServerRestartRequest) SetMode(v *string) { m.mode = v }

// AlertRuleable is the read interface.
type AlertRuleable interface {
	GetId() *string
	SetId(*string)
	GetName() *string
	SetName(*string)
	GetSeverity() *int64
	SetSeverity(*int64)
}

// AlertRule is the concrete model.
type AlertRule struct {
	id       *string
	name     *string
	severity *int64
}

// NewAlertRule constructs an empty model.
func NewAlertRule() *AlertRule { return &AlertRule{} }

func (m *AlertRule) GetId() *string       { return m.id }
func (m *AlertRule) SetId(v *string)      { m.id = v }
func (m *AlertRule) GetName() *string     { return m.name }
func (m *AlertRule) SetName(v *string)    { m.name = v }
func (m *AlertRule) GetSeverity() *int64  { return m.severity }
func (m *AlertRule) SetSeverity(v *int64) { m.severity = v }

// Licenseable is the read interface.
type Licenseable interface {
	GetSeats() *int64
	GetExpires() *time.Time
}

// License is the concrete model.
type License struct {
	seats   *int64
	expires *time.Time
}

// GetSeats answers the seat count.
func (m *License) GetSeats() *int64 { return m.seats }

// GetExpires answers the expiry.
func (m *License) GetExpires() *time.Time { return m.expires }

// AuditEventable is the read interface.
type AuditEventable interface {
	GetId() *string
	GetName() *string
}

// AuditEvent is the concrete model.
type AuditEvent struct {
	id   *string
	name *string
}

// GetId answers the id.
func (m *AuditEvent) GetId() *string { return m.id }

// GetName answers the name.
func (m *AuditEvent) GetName() *string { return m.name }

// AuditEventCollectionResponseable is the list envelope's read interface.
type AuditEventCollectionResponseable interface {
	GetValue() []AuditEventable
}

// AuditEventCollectionResponse is the list envelope.
type AuditEventCollectionResponse struct {
	value []AuditEventable
}

// GetValue answers the elements.
func (m *AuditEventCollectionResponse) GetValue() []AuditEventable { return m.value }
