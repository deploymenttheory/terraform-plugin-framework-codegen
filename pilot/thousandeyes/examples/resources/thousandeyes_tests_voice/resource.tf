resource "thousandeyes_tests_voice" "example" {
  target_agent_id = "tfacc-target-agent-id"
  agents          = [{ agent_id = "3" }]
}
