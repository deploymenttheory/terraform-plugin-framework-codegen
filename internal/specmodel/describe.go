package specmodel

// Describe reports what a document says about itself: the version its info
// object declares, and how much surface it carries.
//
// It satisfies corpus.Describer, which is where a pin's recorded counts come
// from. Those counts are what catch a truncated download that happens to
// parse, so a document this cannot read reports zero rather than a guess.
func Describe(doc []byte) (version string, paths, operations int) {
	loaded, err := Load(doc)
	if err != nil {
		return "", 0, 0
	}
	for _, path := range loaded.Paths {
		operations += len(path.Operations)
	}
	return loaded.Info.Version, len(loaded.Paths), operations
}
