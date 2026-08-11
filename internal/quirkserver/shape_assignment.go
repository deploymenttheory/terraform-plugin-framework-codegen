package quirkserver

import "fmt"

// validateAssignment is the reference-borrowing ground truth. An assignment
// names an agent by id, and the id must belong to an agent the /agents
// collection actually serves:
//
//	agent_id absent or empty            -> "field agent_id is required"
//	agent_id names no served agent      -> "field agent_id must reference an existing agent"
//
// The document declares agent_id a plain required string; that it must
// reference a *live* object is the semantic the audit discovers. The only way
// to satisfy it is to GET /agents and borrow one of the ids it lists -- a
// synthesised id is refused by construction.
//
// Both sentences begin `field agent_id `, so the same `^field (\S+) ` that
// parses a monitor error parses this one, and the "existing agent" tail names
// the collection the executor must go read.
func (s *Server) validateAssignment(body map[string]any) string {
	raw, ok := body["agent_id"]
	if !ok || fmt.Sprint(raw) == "" {
		return "field agent_id is required"
	}
	if !s.agents.has(fmt.Sprint(raw)) {
		return "field agent_id must reference an existing agent"
	}
	return ""
}
