package naming

import "fmt"

// UnitTestName builds the numbered unit-test identifier the archetype provider
// uses. The ordinal makes the intended reading order explicit in the file, and
// the subject is the second underscore-delimited field because the house CI
// workflow's statistics block parses it that way.
//
//	(1, "Tag", "Schema") -> "TestUnit_Tag_01_Schema"
func UnitTestName(ordinal int, subject, kind string) string {
	return fmt.Sprintf("TestUnit_%s_%02d_%s", subject, ordinal, kind)
}

// AccTestName builds the acceptance-test identifier. Acceptance tests are
// prefixed TestAcc so the standard -run filter separates them from unit tests
// that must never touch a network.
//
//	(1, "Tag", "Minimal") -> "TestAcc_Tag_01_Minimal"
func AccTestName(ordinal int, subject, kind string) string {
	return fmt.Sprintf("TestAcc_%s_%02d_%s", subject, ordinal, kind)
}
