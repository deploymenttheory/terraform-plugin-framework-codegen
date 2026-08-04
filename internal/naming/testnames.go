package naming

import "fmt"

// AccTestName builds the acceptance-test identifier every generated test file
// uses. Acceptance tests are prefixed TestAcc so the standard -run filter
// separates them from unit tests that must never touch a network; the block
// kind follows immediately so one resource's tests sort together; the ordinal
// makes the intended reading order explicit in the file.
//
//	("Resource", "Tag", 1, "Lifecycle") -> "TestAccResourceTag_01_Lifecycle"
func AccTestName(blockKind, subject string, ordinal int, scenario string) string {
	return fmt.Sprintf("TestAcc%s%s_%02d_%s", blockKind, subject, ordinal, scenario)
}
