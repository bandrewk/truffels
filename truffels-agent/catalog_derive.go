package main

// All paths and names of the catalog path are constructed here, never taken
// from a request. The "cat-" prefix is a firewall: the new code path cannot
// address /srv/truffels/compose/bitcoin/ — not by convention, but because the
// path is built and not passed in.
//
// Callers may rely on the fact that id has already been validated against
// catalogIDRe.

func catContainerName(id, container string) string {
	return "truffels-" + id + "-" + container
}

func catComposeDir(id string) string {
	return composeRoot + "/cat-" + id
}

func catDataDir(id string) string {
	return dataRoot + "/cat-" + id
}

func catConfigPath(id, file string) string {
	return configRoot + "/cat-" + id + "/" + file
}

// catConfigDir returns the directory containing the rendered config files for
// a catalog entry. Replaces the filepath.Dir(catConfigPath(id,"x")) idiom —
// a dummy filename as a path idiom in the trust boundary invites wrong copies.
func catConfigDir(id string) string {
	return configRoot + "/cat-" + id
}
