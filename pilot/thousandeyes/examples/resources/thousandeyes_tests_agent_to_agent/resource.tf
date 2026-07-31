resource "thousandeyes_tests_agent_to_agent" "example" {
  target_agent_id = "tfacc-target-agent-id"
  agents          = [{ agent_id = "3" }]
}
