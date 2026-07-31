resource "thousandeyes_tests_api" "example" {
  requests = [{ name = "step-1", url = "https://api.stripe.com/healthcheck", method = "get" }]
  url      = "tfacc-url"
  agents   = [{ agent_id = "3" }]
}
