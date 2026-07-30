# This file is yours.
#
# It was scaffolded once, carries no generated marker, and `emit` will not write over it again --
# so your edits survive regeneration and the drift check does not police it.
#
# What it is for: exercising more of the schema than the minimal fixture does. The generator has
# filled in every writable attribute whose value it could derive; what it could not is listed
# inside the block, and filling those in is the point of the file being yours.

resource "thousandeyes_tag" "test" {
  # Where a value came from.
  #
  # color: the API's own default, so a value it is known to accept
  #
  # icon: the API's own default, so a value it is known to accept
  #
  # object_type: observed accepted; the API refused endpoint-agent
  #
  # access_type: observed accepted; the API refused system
  #
  # match_type: observed accepted
  #
  # Not filled in, because no correct value could be derived. Each of these is optional, so this
  # configuration is valid as it stands -- but it is testing less than it could.
  #
  # assignments: a nested object's members may be identifiers of objects that must already
  # exist, and nothing in the specification says whether they are
  #
  # filters: a nested object's members may be identifiers of objects that must already exist,
  # and nothing in the specification says whether they are
  key         = "tfacc-key"
  value       = "tfacc-value"
  color       = "#A7EB10"
  description = "tfacc-description"
  icon        = "LABEL"
  object_type = "test"
  access_type = "all"
  match_type  = "and"
}
