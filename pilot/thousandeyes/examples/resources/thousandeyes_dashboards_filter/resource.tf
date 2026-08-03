resource "thousandeyes_dashboards_filter" "example" {
  context = [{ data_source_id = "VIRTUAL_AGENT", filters = [{ filter_id = "TEST", metric_ids = [], values = ["281474976977085"] }] }]
  name    = "tfacc-name"
}
