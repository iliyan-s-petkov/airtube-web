package eea

// LastFileFetchCountForTesting exposes lastFileFetch's size to the external
// test package, so a pruning test can assert the map stays bounded without
// reaching into an unexported field.
func (c *Collector) LastFileFetchCountForTesting() int {
	return len(c.lastFileFetch)
}
