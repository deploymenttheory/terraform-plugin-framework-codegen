package main

// blueprintVerbs is the blueprint group's verb table. draft and merge are the
// two built stages; validate, diff and list are registered up front so the help
// output describes the intended surface and a user who reaches for a missing
// verb gets a straight answer instead of "unknown verb".
var blueprintVerbs = []command{
	{
		name:    "draft",
		summary: "infer draft blueprints from an OpenAPI snapshot",
		usage:   usageBlueprintDraft,
		run:     runBlueprintDraft,
	},
	{
		name:    "merge",
		summary: "fold probe facts into a blueprint",
		usage:   usageBlueprintMerge,
		run:     runBlueprintMerge,
	},
	{
		name:    "validate",
		summary: "validate blueprints without generating anything",
		usage:   "blueprint validate [flags]",
		run:     notImplemented("blueprint validate"),
	},
	{
		name:    "diff",
		summary: "diff two blueprints",
		usage:   "blueprint diff [flags]",
		run:     notImplemented("blueprint diff"),
	},
	{
		name:    "list",
		summary: "list the blueprints a directory holds",
		usage:   "blueprint list [flags]",
		run:     notImplemented("blueprint list"),
	},
}

func runBlueprint(args []string) error {
	return runVerbs("blueprint", blueprintVerbs, "", args)
}
